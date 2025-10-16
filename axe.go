package axe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
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
	DefaultMaxSteps         = 40
	DefaultHistoryFile      = ".axe_history.xml"
	DefaultOutputBufferSize = 4096
)

type RunnerState struct {
	Code    *container.CodeContainer // Always only one code container
	Outputs []container.CodeOutput   // Outputs from the agent
}

// Runner is the core workflow executor.
// Agent / logger -> write_to -> Output channel -> OutputRecorder
// OutputRecorder fan out to the sink and a string buffer.
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
	Output chan string // output buffer for the agent's output. This will be consumed by outputRecorder.
	Sink   io.Writer   // the sink to write the agent's output to

	KeepHistory bool // if true, previous changelogs will be kept.

	outputRecorder *outputRecorder // the recorder to record the agent's output to a string buffer & write to sink
	wg             sync.WaitGroup
}

func NewRunner(baseDir string, instructions []string, code *container.CodeContainer, opts ...RunnerOption) (*Runner, error) {
	r := &Runner{
		BaseDir:      baseDir,
		Instructions: instructions,
		State: &RunnerState{
			Code: code,
		},
		Output: make(chan string, DefaultOutputBufferSize),
	}
	for _, opt := range opts {
		if err := opt(r); err != nil {
			return nil, err
		}
	}
	if err := r.applyDefaults(); err != nil {
		return nil, err
	}
	r.outputRecorder = &outputRecorder{
		sink: r.Sink,
	}
	return r, nil
}

func (r *Runner) applyDefaults() error {
	if r.History == nil {
		historyFile := filepath.Join(r.BaseDir, DefaultHistoryFile)
		history, err := history.ReadHistoryFromFile(historyFile)
		if err != nil {
			return fmt.Errorf("axe: read history file: %w", err)
		}
		r.History = history
	}
	if r.MaxSteps <= 0 {
		r.MaxSteps = DefaultMaxSteps
	}
	if r.Model == "" {
		r.Model = ModelGPT4o
	}
	return nil
}

func (r *Runner) Run(ctx context.Context, loadDotEnv bool) error {
	if r == nil {
		return errors.New("axe: nil runner")
	}
	if loadDotEnv {
		if err := godotenv.Load(); err != nil {
			log.Warn().Err(err).Msg("axe: load .env file")
		}
	}
	if r.shouldSkipRun() {
		return nil
	}

	// spawn a goroutine to consume the output from the agent and write to the outputRecorder. This goroutine will exit when Output is closed.
	closeOutputOnce := sync.OnceFunc(func() {
		close(r.Output)
	})
	defer closeOutputOnce()
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.outputRecorder.consume(r.Output)
	}()

	chatModel, err := newChatModel(ctx, r.Model)
	if err != nil {
		return err
	}
	log.Debug().Msgf("axe: using model %s", r.Model)
	r.outputRecorder.Write(fmt.Sprintf("axe: using model %s\n", r.Model))

	changelog := history.Changelog{Timestamp: time.Now()}
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
	for _, msg := range messages {
		r.outputRecorder.Write(fmt.Sprintf("%s: %s\n", msg.Role, msg.Content))
	}

	msgReader, err := agt.Stream(ctx, messages)
	if err != nil {
		return fmt.Errorf("axe: agent execution failed: %w", err)
	}
	defer msgReader.Close()

	agentExecErr := r.consumeAgentStream(msgReader)
	log.Debug().Err(agentExecErr).Msg("axe: agent execution finished")

	if agentExecErr != nil {
		r.outputRecorder.Write(fmt.Sprintf("Agent execution failed: %v\n", agentExecErr))
	} else {
		r.outputRecorder.Write("Agent execution finished successfully.\n")
	}

	// time to close output and wait for the outputRecorder to finish.
	closeOutputOnce()
	r.wg.Wait()

	// after close, write the outputRecorder's string buffer to the changelog.
	if output := r.outputRecorder.String(); output != "" {
		changelog.AddLog(output)
	}

	if !r.KeepHistory {
		// clear previous changelogs
		r.History.Changelogs = []history.Changelog{changelog}
	} else {
		r.History.AppendChangelog(changelog)
	}
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
		maxSteps = DefaultMaxSteps
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

// toolCallChecker observes streamed messages from the model and proxies them to the runner output channel.
func (r *Runner) toolCallChecker(_ context.Context, sr *schema.StreamReader[*schema.Message]) (bool, error) {
	defer sr.Close()
	hasToolCalls := false
	lastToolCallID := ""
	var callStreamer *ToolCallStreamer
	defer func() {
		if callStreamer != nil {
			_ = callStreamer.Close()
		}
	}()
	for {
		msg, err := sr.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return false, err
		}
		log.Debug().Str("type", fmt.Sprintf("%T", msg)).Any("msg", msg).Msg("stream msg")

		if len(msg.ToolCalls) > 0 {
			hasToolCalls = true
			if len(msg.ToolCalls) > 1 {
				// XXX(yxia): we don't support stream multiple tool calls yet.
				// I am not even sure if model would stream multiple tool calls at once.
				// Even model has multiple tool calls, I assume it will return them one by one.
				// Anyways, in this case, we just simply stream the message.
				r.streamFrame(msg)
			} else {
				call := msg.ToolCalls[0]
				if call.ID != "" && call.ID != lastToolCallID {
					// close the previous call streamer and create a new one
					if callStreamer != nil {
						_ = callStreamer.Close()
					}
					lastToolCallID = call.ID
					callStreamer = NewToolCallStreamer(call.ID, r.Output)
				}
				err := callStreamer.OnMsg(&call)
				if err != nil {
					// unexpected error, just return
					return false, err
				}
			}
		} else {
			r.streamFrame(msg)
		}
	}
	r.Output <- "\n"
	return hasToolCalls, nil
}

func (r *Runner) streamFrame(frame any) {
	switch frame := frame.(type) {
	case *schema.Message:
		if frame.Content != "" {
			r.outputRecorder.Write(frame.Content)
		} else if len(frame.ToolCalls) > 0 {
			panic("tool calls in message")
		}
	}
}

// outputRecorder is a helper to record the output and write it to a sink.
type outputRecorder struct {
	mu   sync.Mutex
	buf  strings.Builder
	sink io.Writer
}

func (o *outputRecorder) Write(text string) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.buf.WriteString(text)
	if o.sink != nil {
		_, err := io.WriteString(o.sink, text)
		if err != nil {
			log.Error().Err(err).Msg("axe: write to sink")
		}
	}
}

func (o *outputRecorder) String() string {
	if o == nil {
		return ""
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.String()
}

// consume consumes the output from the agent and writes it to the outputRecorder. This function will exit when the out channel is closed.
func (o *outputRecorder) consume(out chan string) {
	for msg := range out {
		o.Write(msg)
	}
}
