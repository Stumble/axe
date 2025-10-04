package axe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/joho/godotenv"
	"github.com/rs/zerolog/log"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/prompt"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"

	"github.com/stumble/axe/code/container"
	clitool "github.com/stumble/axe/tools/cli"
	"github.com/stumble/axe/tools/code"
	"github.com/stumble/axe/tools/finalize"
)

type ModelName string

const (
	ModelGPT5      ModelName = "gpt-5"
	ModelGPT4o     ModelName = "gpt-4o"
	ModelGPT4oMini ModelName = "gpt-4o-mini"
)

const (
	OpenAIDefaultBaseURL = "https://api.openai.com/v1"
	defaultMaxSteps      = 40
)

type RunnerState struct {
	Code    *container.CodeContainer // Always only one code container
	Outputs []container.CodeOutput   // Outputs from the agent
}

// Runner is the core workflow executor.
type Runner struct {
	HistoryFile  string
	Instructions []string
	Model        ModelName
	Workdir      string // TODO: execute command in workdir
	MaxSteps     int
	// CLI tools that the agent can call
	Tools []clitool.Definition

	// The state of the runner
	State  *RunnerState
	Output chan string // output from the agent
}

type RunnerOption func(*Runner)

func WithModel(model ModelName) RunnerOption {
	return func(r *Runner) {
		r.Model = model
	}
}

func WithWorkdir(workdir string) RunnerOption {
	return func(r *Runner) {
		r.Workdir = workdir
	}
}

func WithMaxSteps(maxSteps int) RunnerOption {
	return func(r *Runner) {
		r.MaxSteps = maxSteps
	}
}

func WithTools(tools []clitool.Definition) RunnerOption {
	return func(r *Runner) {
		r.Tools = tools
	}
}

func NewRunner(historyFile string, instructions []string, code *container.CodeContainer, opts ...RunnerOption) *Runner {
	r := &Runner{
		HistoryFile:  historyFile,
		Instructions: instructions,
		State: &RunnerState{
			Code: code,
		},
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func (r *Runner) Run(ctx context.Context, loadDotEnv bool) error {
	if r == nil {
		return errors.New("axe: nil runner")
	}

	if loadDotEnv {
		err := godotenv.Load()
		if err != nil {
			log.Warn().Err(err).Msg("axe: load .env file")
		}
	}

	chatModel, err := newChatModel(ctx, r.Model)
	if err != nil {
		return err
	}
	log.Info().Msgf("axe: using model %s", r.Model)

	tools := []tool.BaseTool{
		&code.ApplyEditTool{Code: r.State.Code}, // apply code output to code container, code output is the parameter
		&finalize.FinalizeTool{},                // finalize the task
	}

	maxSteps := r.MaxSteps
	if maxSteps <= 0 {
		maxSteps = defaultMaxSteps
	}

	agentCfg := &react.AgentConfig{
		StreamToolCallChecker: toolCallChecker,
		ToolCallingModel: chatModel,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools:               tools,
			ExecuteSequentially: true, // execute tools sequentially. We will also prompt LLM to do the code edit tool before other tools like tests, if llm prefers to multi-call them.
			// Note: ToolArgumentsHandler is not used in this version.
			// ToolArgumentsHandler: func(ctx context.Context, name, arguments string) (string, error) {
			// 	log.Debug().Str("name", name).Str("arguments", arguments).Msg("ToolArgumentsHandler")
			// 	switch name {
      //   case code.ApplyEditToolName:
			// 		arg := make(map[string]string)
			// 		_ = json.Unmarshal([]byte(arguments), &arg)
			// 		fmt.Printf("apply edit tool arguments: %s\n", arg["code_output"])
			// 	case finalize.FinalizeToolName:
			// 		return arguments, nil
			// 	default:
			// 		return arguments, nil
			// 	}
			// 	return arguments, nil
			// },
			UnknownToolsHandler: func(ctx context.Context, name, input string) (string, error) {
				log.Fatal().Str("name", name).Str("input", input).Msg("UnknownToolsHandler")
				return "", nil
			},
		},
		MaxStep:            maxSteps,
		ToolReturnDirectly: map[string]struct{}{finalize.FinalizeToolName: {}},
		MessageModifier: func(ctx context.Context, input []*schema.Message) []*schema.Message {
			// TODO: add context trimming here, e.g., remove previous code snippets.
			return input
		},
	}

	agt, err := react.NewAgent(ctx, agentCfg)
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
		fmt.Printf("%s: %s\n", msg.Role, msg.Content)
	}

	r.Output = make(chan string, 4096)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-r.Output:
				if !ok {
					return
				}
				fmt.Printf("%s", msg)
			}
		}
	}()

	var builder strings.Builder
	var msgReader *schema.StreamReader[*schema.Message]
	opt := []agent.AgentOption{
		agent.WithComposeOptions(compose.WithCallbacks(&LoggerCallback{Output: r.Output})),
	}
	msgReader, err = agt.Stream(ctx, messages, opt...)
	if err != nil {
		return fmt.Errorf("axe: agent execution failed: %w", err)
	}
	defer msgReader.Close()
	for {
		// msg type is *schema.Message
		msg, err := msgReader.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				// finish
				break
			}
			// error
			log.Error().Err(err).Msg("axe: agent execution failed")
			return fmt.Errorf("axe: agent execution failed: %w", err)
		}

		builder.WriteString(msg.Content)
	}
	log.Debug().Str("content", builder.String()).Msg("axe: agent execution finished")
	return nil
}

func newChatModel(ctx context.Context, desiredModel ModelName) (model.ToolCallingChatModel, error) {
	apiKey := strings.TrimSpace(os.Getenv("OAI_MY_KEY"))
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	}
	if apiKey == "" {
		return nil, errors.New("axe: missing OpenAI API key; set OAI_MY_KEY or OPENAI_API_KEY")
	}

	baseURL := strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
	if baseURL == "" {
		baseURL = OpenAIDefaultBaseURL
	}
	temp := float32(0)

	chatModel, err := einoopenai.NewChatModel(ctx, &einoopenai.ChatModelConfig{
		APIKey:      apiKey,
		BaseURL:     baseURL,
		Model:       string(desiredModel),
		Temperature: &temp,
	})
	if err != nil {
		return nil, fmt.Errorf("axe: create chat model: %w", err)
	}
	return chatModel, nil
}

// prompt construction moved to eino prompt helpers above
func buildInitialMessages(ctx context.Context, r *Runner, codeInputXML string) ([]*schema.Message, error) {
	// Jinja2 templates using Go's multiline raw strings
	sys := `You are Axe, a focused ReAct coding assistant. Use the available tools to follow the user's instruction exactly.

Tooling rules:
1. To change code, call {apply_tool} with the full desired content of each file.
2. Finish only by calling {finalize_tool} with status 'success' once the instruction is satisfied. If you cannot complete the task, call it with status 'failure' and explain why.
{{ cli_hint }}Reason about the plan before calling tools, cite file paths explicitly, and avoid editing files that were not provided.`

	usr := `Instruction: {{ instruction }}

CodeInput: {{ code_input }}`

	template := prompt.FromMessages(schema.Jinja2,
		schema.SystemMessage(sys),
		schema.UserMessage(usr),
	)

	cliHint := ""
	if len(r.Tools) > 0 {
		cliHint = "Additionally, you can call user-provided CLI tools when needed. Choose the appropriate tool at the right time.\n"
	}
	instruction := strings.TrimSpace(strings.Join(r.Instructions, "\n"))

	vars := map[string]any{
		"apply_tool":    code.ApplyEditToolName,
		"finalize_tool": finalize.FinalizeToolName,
		"cli_hint":      cliHint,
		"instruction":   instruction,
		"code_input":    codeInputXML,
	}

	return template.Format(ctx, vars)
}

// LoggerCallback logs detailed lifecycle and streaming information from the agent run.
type LoggerCallback struct {
	callbacks.HandlerBuilder
	Output chan<- string
}

func (cb *LoggerCallback) OnStart(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
	data, _ := json.MarshalIndent(input, "", "  ")
	log.Debug().Str("name", info.Name).RawJSON("input", data).Msg("OnStart")
	// cb.Output <- fmt.Sprintf("(%s) On Start: %s\n", info.Name, string(data))
	return ctx
}

func (cb *LoggerCallback) OnEnd(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
	data, _ := json.MarshalIndent(output, "", "  ")
	log.Debug().Str("name", info.Name).RawJSON("output", data).Msg("OnEnd")
	cb.Output <- fmt.Sprintf("(%s) On End: %s\n", info.Name, string(data))
	return ctx
}

func (cb *LoggerCallback) OnError(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
	log.Error().Str("name", info.Name).Err(err).Msg("OnError")
	return ctx
}

func (cb *LoggerCallback) OnEndWithStreamOutput(ctx context.Context, info *callbacks.RunInfo, output *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
	if info.Name != react.GraphName {
		return ctx
	}
	log.Debug().Str("name", info.Name).Msg("CallingOnEndWithStreamOutput")
	go func() {
		defer func() { _ = recover() }()
		defer output.Close()
		for {
			frame, err := output.Recv()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				log.Error().Str("name", info.Name).Err(err).Msg("stream recv error")
				return
			}
			data, err := json.Marshal(frame)
			if err != nil {
				log.Error().Str("name", info.Name).Err(err).Msg("stream marshal error")
				return
			}
			// Print out type of frame
			log.Debug().Str("name", info.Name).Str("type", fmt.Sprintf("%T", frame)).Msg("OnEndStream")
			log.Debug().Str("name", info.Name).RawJSON("stream_frame", data).Msg("OnEndStream")
			// (ReActAgent) On End Stream: {"role":"assistant","content":"","tool_calls":[{"index":0,"id":"call_L19OTaP8nB9ESfSt4b2Vt4Jj","type":"function","function":{"name":"apply_edit"}}],"response_meta":{}}
			switch frame := frame.(type) {
			case *schema.Message:
				if frame.Content != "" {
					cb.Output <- frame.Content
				} else if len(frame.ToolCalls) > 0 {
					for _, toolCall := range frame.ToolCalls {
						if toolCall.ID != "" {
							cb.Output <- fmt.Sprintf("\ntool call id: %s\n", toolCall.ID)
						}
						if toolCall.Function.Name != "" {
							cb.Output <- fmt.Sprintf("tool call function name: %s\n", toolCall.Function.Name)
							cb.Output <- "Arguments:\n"
						}
						cb.Output <- toolCall.Function.Arguments
					}
				}
			default:
				log.Debug().Str("name", info.Name).Str("type", fmt.Sprintf("%T", frame)).Msg("OnEndStream")
			}
		}
	}()
	return ctx
}

func (cb *LoggerCallback) OnStartWithStreamInput(ctx context.Context, info *callbacks.RunInfo, input *schema.StreamReader[callbacks.CallbackInput]) context.Context {
	go func() {
		defer func() { _ = recover() }()
		defer input.Close()
		for {
			frame, err := input.Recv()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				log.Error().Str("name", info.Name).Err(err).Msg("stream send error")
				return
			}
			data, _ := json.Marshal(frame)
			log.Debug().Str("name", info.Name).RawJSON("stream_input", data).Msg("OnStartStream")
			// cb.Output <- fmt.Sprintf("(%s) On Start Stream: %s\n", info.Name, string(data))
		}
	}()
	return ctx
}

func toolCallChecker(_ context.Context, sr *schema.StreamReader[*schema.Message]) (bool, error) {
    defer sr.Close()
    for {
       msg, err := sr.Recv()
       if err != nil {
          if errors.Is(err, io.EOF) {
             // finish
             break
          }

          return false, err
       }

       if len(msg.ToolCalls) > 0 {
          return true, nil
       }
    }
    return false, nil
}