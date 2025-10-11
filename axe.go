package axe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog/log"

	"github.com/stumble/axe/code/container"
	"github.com/stumble/axe/history"
	clitool "github.com/stumble/axe/tools/cli"
	"github.com/stumble/axe/tools/code"
	"github.com/stumble/axe/tools/finalize"
)

const (
	defaultMaxSteps    = 40
	DefaultHistoryFile = ".axe_history.xml"
)

type RunnerState struct {
	Code    *container.CodeContainer // Always only one code container
	Outputs []container.CodeOutput   // Outputs from the agent
}

// Runner is the core workflow executor.
type Runner struct {
	BaseDir      string // a base directory, relative to the current working directory.
	History      *history.History
	MinInterval  time.Duration // if > 0, skip run when last edit is within this duration
	Instructions []string
	Model        ModelName
	MaxSteps     int
	// CLI tools that the agent can call
	Tools []clitool.Definition

	// The state of the runner
	State  *RunnerState
	Output chan string // output from the agent
}

func NewRunner(baseDir string, instructions []string, code *container.CodeContainer, opts ...RunnerOption) (*Runner, error) {
	r := &Runner{
		BaseDir:      baseDir,
		Instructions: instructions,
		State: &RunnerState{
			Code: code,
		},
	}
	for _, opt := range opts {
		if err := opt(r); err != nil {
			return nil, err
		}
	}
	if err := r.applyDefaults(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Runner) Run(ctx context.Context, loadDotEnv bool) error {
	if r == nil {
		return errors.New("axe: nil runner")
	}
	if r.shouldSkipRun() {
		return nil
	}
	if loadDotEnv {
		if err := godotenv.Load(); err != nil {
			log.Warn().Err(err).Msg("axe: load .env file")
		}
	}

	recorder := &outputRecorder{}

	chatModel, err := newChatModel(ctx, r.Model)
	if err != nil {
		return err
	}
	log.Debug().Msgf("axe: using model %s", r.Model)
	r.writeStdout(recorder, fmt.Sprintf("axe: using model %s\n", r.Model))

	changelog := history.Changelog{Timestamp: time.Now()}
	r.Output = make(chan string, 4096)
	tools := r.buildToolset(&changelog)

	agt, err := react.NewAgent(ctx, r.buildAgentConfig(chatModel, tools))
	if err != nil {
		return fmt.Errorf("axe: create agent: %w", err)
	}

	initialState, err := r.State.Code.BuildCodeInput(nil).ToXML()
	if err != nil {
		return fmt.Errorf("axe: build code input: %w", err)
	}
	messages, err := buildInitialMessages(ctx, r, initialState)
	if err != nil {
		return fmt.Errorf("axe: format prompt: %w", err)
	}
	r.printInitialMessages(messages, recorder)

	wg := r.startOutputForwarder(ctx, recorder)

	msgReader, err := agt.Stream(ctx, messages)
	if err != nil {
		return fmt.Errorf("axe: agent execution failed: %w", err)
	}
	defer msgReader.Close()

	agentExecErr := r.consumeAgentStream(msgReader)
	close(r.Output)
	log.Debug().Err(agentExecErr).Msg("axe: agent execution finished")

	if agentExecErr != nil {
		recorder.Write(fmt.Sprintf("Agent execution failed: %v\n", agentExecErr))
	} else {
		recorder.Write("Agent execution finished successfully.\n")
	}

	wg.Wait()

	if output := recorder.String(); output != "" {
		changelog.AddLog(output)
	}

	r.History.AppendChangelog(changelog)
	if err := r.History.SaveHistoryToFile(); err != nil {
		return fmt.Errorf("axe: save history: %w", err)
	}
	return nil
}

func (r *Runner) shouldSkipRun() bool {
	if r.MinInterval == 0 {
		return false
	}
	if ts, ok := r.History.LastChangelogTimestamp(); ok {
		if time.Since(ts) < r.MinInterval {
			log.Info().Msgf("axe: skipping run, last edit %s ago < min interval %s", time.Since(ts).String(), r.MinInterval.String())
			return true
		}
	}
	return false
}

func (r *Runner) buildToolset(changelog *history.Changelog) []tool.BaseTool {
	tools := []tool.BaseTool{
		&code.ApplyEditTool{Code: r.State.Code},
		&finalize.FinalizeTool{Changelog: changelog},
	}
	for _, cli := range r.Tools {
		tools = append(tools, &clitool.CliTool{Def: cli})
	}
	return tools
}

func (r *Runner) buildAgentConfig(chatModel model.ToolCallingChatModel, tools []tool.BaseTool) *react.AgentConfig {
	maxSteps := r.MaxSteps
	if maxSteps <= 0 {
		maxSteps = defaultMaxSteps
	}
	return &react.AgentConfig{
		StreamToolCallChecker: r.toolCallChecker,
		ToolCallingModel:      chatModel,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools:               tools,
			ExecuteSequentially: true,
			UnknownToolsHandler: func(ctx context.Context, name, input string) (string, error) {
				log.Fatal().Str("name", name).Str("input", input).Msg("UnknownToolsHandler")
				return "", nil
			},
		},
		MaxStep:            maxSteps,
		ToolReturnDirectly: map[string]struct{}{finalize.FinalizeToolName: {}},
		MessageModifier: func(ctx context.Context, input []*schema.Message) []*schema.Message {
			if len(input) > 0 {
				last := input[len(input)-1]
				if last.Role == schema.Tool {
					log.Debug().Msgf("Tool call response: %s\n", last)
					r.Output <- fmt.Sprintf("Tool call response: %s\n", last.Content)
				}
			}
			return input
		},
	}
}

func (r *Runner) printInitialMessages(messages []*schema.Message, recorder *outputRecorder) {
	for _, msg := range messages {
		r.writeStdout(recorder, fmt.Sprintf("%s: %s\n", msg.Role, msg.Content))
	}
}

func (r *Runner) startOutputForwarder(ctx context.Context, recorder *outputRecorder) *sync.WaitGroup {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-r.Output:
				if !ok {
					return
				}
				r.writeStdout(recorder, msg)
			}
		}
	}()
	return &wg
}

func (r *Runner) writeStdout(recorder *outputRecorder, text string) {
	fmt.Print(text)
	if recorder != nil {
		recorder.Write(text)
	}
}

type outputRecorder struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (o *outputRecorder) Write(text string) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.buf.WriteString(text)
}

func (o *outputRecorder) String() string {
	if o == nil {
		return ""
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.String()
}

func (r *Runner) consumeAgentStream(msgReader *schema.StreamReader[*schema.Message]) error {
	var agentExecErr error
	for {
		if _, err := msgReader.Recv(); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			log.Error().Err(err).Msg("axe: agent execution failed")
			agentExecErr = err
			break
		}
	}
	return agentExecErr
}
