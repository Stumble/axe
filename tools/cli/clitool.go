package clitool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/mattn/go-shellwords"
	"github.com/rs/zerolog/log"
)

// Definition describes a CLI tool that can be exposed to the agent.
// Name must be unique across all tools.
// Command is executed without a shell by default via SubprocessExecutor.
type Definition struct {
	Name    string
	Command string
	Desc    string
	Args    []string // parsed from command
	Env     map[string]string // merged with envs from command, env map has higher precedence than envs from command.
}

func MustNewDefinition(name, command, desc string, env map[string]string) Definition {
	def, err := NewDefinition(name, command, desc, env)
	if err != nil {
		panic(err)
	}
	return def
}

func NewDefinition(name, command, desc string, env map[string]string) (Definition, error) {
	// Parse base command into env assignments and argv
	cmdEnvs, args, err := shellwords.ParseWithEnvs(command)
	if err != nil {
		return Definition{}, err
	}

	// Merge envs: definition env overlaid by command-line env assignments
	env = MergeEnv(parseEnvKVs(cmdEnvs), env)

	return Definition{
		Name:    name,
		Command: command,
		Desc:    desc,
		Args:    args,
		Env:     env,
	}, nil
}

// Outcome describes the result of a subprocess execution.
type Outcome struct {
	Ran             bool
	Command         string
	ExitCode        int
	Duration        time.Duration
	Stdout          string
	Stderr          string
	StartedAt       time.Time
	CompletedAt     time.Time
}

// SubprocessExecutor runs commands using exec.CommandContext without a shell.
type SubprocessExecutor struct{}

func (e *SubprocessExecutor) Execute(ctx context.Context, argv []string, env map[string]string, workdir string) (Outcome, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = workdir
	cmd.Env = append(os.Environ(), flattenEnv(env)...)

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	exitCode := 0
	isTimedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
	var exitErr *exec.ExitError
	if err != nil {
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else if isTimedOut {
			exitCode = -1
		} else {
			exitCode = 1
		}
	}

	stdout := stdoutBuf.String()
	stderr := stderrBuf.String()

	if err != nil && exitErr == nil && !isTimedOut {
		if stderr != "" && !strings.HasSuffix(stderr, "\n") {
			stderr += "\n"
		}
		stderr += "command error: " + err.Error()
	}

	outcome := Outcome{
		Ran:         true,
		Command:     strings.Join(argv, " "),
		ExitCode:    exitCode,
		Duration:    duration,
		Stdout:      justClipString(stdout, 3000),
		Stderr:      justClipString(stderr, 3000),
		StartedAt:   start,
		CompletedAt: start.Add(duration),
	}
	return outcome, nil
}

// ---------------------- Tool integration for LLM invocation ----------------------

// Tool exposes a configured CLI command as an invocable tool to the model.
// Name and description are derived from Definition. Users can provide additional
// argv, env, workdir, and timeout at call-time.
type CliTool struct {
	// Def describes the base command configuration.
	Def Definition
	// Workdir is the default working directory when not overridden by request.
	Workdir string
}

type CliToolRequest struct {
	// Args are extra argv to append to the configured command.
	Args []string `json:"args,omitempty"`
}

// Info describes the tool to the model.
func (t *CliTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: t.Def.Name,
		Desc: t.Def.Desc,
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"args": {
				Type:     schema.Array,
				ElemInfo: &schema.ParameterInfo{Type: schema.String},
				Desc:     "Additional arguments to append to the configured command.",
			},
		}),
	}, nil
}

// InvokableRun executes the configured command with request overrides and returns a JSON outcome.
func (t *CliTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	log.Debug().Msgf("clitool: executing command: %s with arguments: %s", t.Def.Command, argumentsInJSON)
	if strings.TrimSpace(argumentsInJSON) == "" {
		argumentsInJSON = "{}"
	}

	var req CliToolRequest
	if err := json.Unmarshal([]byte(argumentsInJSON), &req); err != nil {
		return fmt.Sprintf("clitool: invalid arguments: %v", err), nil
	}

	argv := append(append([]string{}, t.Def.Args...), req.Args...)
	if len(argv) == 0 {
		return "", errors.New("clitool: empty command")
	}

	// Resolve workdir (no per-call override here)
	workdir := t.Workdir

	// Execute
	exec := &SubprocessExecutor{}
	outcome, _ := exec.Execute(ctx, argv, t.Def.Env, workdir)

	// Render outcome as JSON string for model consumption
	payload, jerr := json.Marshal(outcome)
	if jerr != nil {
		return "", jerr
	}
	return string(payload), nil
}

func parseEnvKVs(pairs []string) map[string]string {
	if len(pairs) == 0 {
		return nil
	}
	out := make(map[string]string, len(pairs))
	for _, kv := range pairs {
		if kv = strings.TrimSpace(kv); kv == "" {
			continue
		}
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			// skip malformed entries silently to be tolerant for LLM outputs
			continue
		}
		k := strings.TrimSpace(kv[:eq])
		v := kv[eq+1:]
		out[k] = v
	}
	return out
}

func flattenEnv(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(env))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}

// MergeEnv returns a new map with base overlaid by overrides.
func MergeEnv(base, overrides map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overrides {
		out[k] = v
	}
	return out
}

const truncatedMarker = "\n...truncated...\n"

func clipString(input string, limit int) (string, bool) {
	if limit <= 0 {
		return input, false
	}
	runes := []rune(input)
	if len(runes) <= limit {
		return input, false
	}
	half := limit / 2
	if half == 0 {
		half = limit
	}
	prefix := string(runes[:half])
	suffix := string(runes[len(runes)-half:])
	return prefix + truncatedMarker + suffix, true
}

func justClipString(input string, limit int) string {
	s, _ := clipString(input, limit)
	return s
}
