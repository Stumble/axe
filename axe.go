package axe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"
	"github.com/yargevad/filepathx"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
)

const (
	ModelGPT5      = "gpt-5"
	ModelGPT4o     = "gpt-4o"
	ModelGPT4oMini = "gpt-4o-mini"
)

const (
	defaultMaxSteps    = 40
	maxTestOutputRunes = 6000
	truncatedMarker    = "\n...truncated...\n"
)

var defaultTestTimeout = 10 * time.Minute

const (
	getProjectStateToolName = "get_project_state"
	applyEditsToolName      = "apply_edits"
	runTestsToolName        = "run_tests"
	finalizeToolName        = "finalize_task"
)

var xmlAttrEscaper = strings.NewReplacer(
	"&", "&amp;",
	"\"", "&quot;",
	"'", "&apos;",
	"<", "&lt;",
	">", "&gt;",
)

// Runner is the core workflow executor.
type Runner struct {
	Instruction string
	Files       map[string]string
	Test        string
	Model       string
	Workdir     string
	MaxSteps    int
	TestTimeout time.Duration
}

type runnerState struct {
	mu          sync.RWMutex
	files       map[string]string
	testCmd     string
	workdir     string
	testTimeout time.Duration

	lastTest      *testOutcome
	finished      bool
	finishStatus  string
	finishSummary string
}

type testOutcome struct {
	Ran             bool
	Command         string
	ExitCode        int
	Duration        time.Duration
	Stdout          string
	Stderr          string
	StdoutTruncated bool
	StderrTruncated bool
	TimedOut        bool
	StartedAt       time.Time
	CompletedAt     time.Time
}

func (r *Runner) Run() error {
	if r == nil {
		return errors.New("axe: nil runner")
	}
	if err := r.validate(); err != nil {
		return err
	}

	_ = godotenv.Load()

	state, err := newRunnerState(r)
	if err != nil {
		return err
	}

	ctx := context.Background()
	chatModel, usedModel, err := newChatModel(ctx, r.Model)
	if err != nil {
		return err
	}
	r.Model = usedModel

	tools := []tool.BaseTool{
		&getProjectStateTool{state: state},
		&applyEditsTool{state: state},
		&runTestsTool{state: state},
		&finalizeTool{state: state},
	}

	maxSteps := r.MaxSteps
	if maxSteps <= 0 {
		maxSteps = defaultMaxSteps
	}

	agentCfg := &react.AgentConfig{
		ToolCallingModel: chatModel,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools: tools,
		},
		MaxStep:            maxSteps,
		ToolReturnDirectly: map[string]struct{}{finalizeToolName: {}},
	}

	agt, err := react.NewAgent(ctx, agentCfg)
	if err != nil {
		return fmt.Errorf("axe: create agent: %w", err)
	}

	initialState := state.renderProjectState(nil)
	messages := []*schema.Message{
		{
			Role:    schema.System,
			Content: buildSystemPrompt(r),
		},
		{
			Role:    schema.User,
			Content: buildUserPrompt(r.Instruction, state.getTestCommand(), initialState),
		},
	}

	if _, err := agt.Generate(ctx, messages); err != nil {
		return fmt.Errorf("axe: agent execution failed: %w", err)
	}

	if err := state.validateCompletion(); err != nil {
		return err
	}

	r.Files = state.snapshotFiles()
	return nil
}

func (r *Runner) validate() error {
	if strings.TrimSpace(r.Instruction) == "" {
		return errors.New("axe: instruction cannot be empty")
	}
	if len(r.Files) == 0 {
		return errors.New("axe: files map cannot be empty")
	}
	return nil
}

func newRunnerState(r *Runner) (*runnerState, error) {
	normalized := make(map[string]string, len(r.Files))
	for path, content := range r.Files {
		canon, err := sanitizeRelativePath(path)
		if err != nil {
			return nil, fmt.Errorf("axe: invalid file path %q: %w", path, err)
		}
		if _, exists := normalized[canon]; exists {
			return nil, fmt.Errorf("axe: duplicate file path after normalization: %s", canon)
		}
		normalized[canon] = content
	}
	r.Files = copyStringMap(normalized)

	workdir := strings.TrimSpace(r.Workdir)
	if workdir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("axe: determine working directory: %w", err)
		}
		workdir = wd
	} else if !filepath.IsAbs(workdir) {
		abs, err := filepath.Abs(workdir)
		if err != nil {
			return nil, fmt.Errorf("axe: resolve workdir %q: %w", workdir, err)
		}
		workdir = abs
	}

	timeout := r.TestTimeout
	if timeout <= 0 {
		timeout = defaultTestTimeout
	}

	return &runnerState{
		files:       copyStringMap(normalized),
		testCmd:     strings.TrimSpace(r.Test),
		workdir:     workdir,
		testTimeout: timeout,
	}, nil
}

func newChatModel(ctx context.Context, desiredModel string) (model.ToolCallingChatModel, string, error) {
	apiKey := strings.TrimSpace(os.Getenv("OAI_MY_KEY"))
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	}
	if apiKey == "" {
		return nil, "", errors.New("axe: missing OpenAI API key; set OAI_MY_KEY or OPENAI_API_KEY")
	}

	modelName := strings.TrimSpace(desiredModel)
	if modelName == "" {
		modelName = strings.TrimSpace(os.Getenv("OPENAI_MODEL_NAME"))
	}
	if modelName == "" {
		modelName = ModelGPT4o
	}

	baseURL := strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
	temp := float32(0)

	chatModel, err := einoopenai.NewChatModel(ctx, &einoopenai.ChatModelConfig{
		APIKey:      apiKey,
		BaseURL:     baseURL,
		Model:       modelName,
		Temperature: &temp,
	})
	if err != nil {
		return nil, "", fmt.Errorf("axe: create chat model: %w", err)
	}
	return chatModel, modelName, nil
}

func buildSystemPrompt(r *Runner) string {
	var sb strings.Builder
	sb.WriteString("You are Axe, a focused ReAct coding assistant. Use the available tools to follow the user's instruction exactly.\n\n")
	sb.WriteString("Tooling rules:\n")
	sb.WriteString(fmt.Sprintf("1. Call `%s` whenever you need the latest file contents or test history.\n", getProjectStateToolName))
	sb.WriteString(fmt.Sprintf("2. To change code, call `%s` with the full desired content of each file.\n", applyEditsToolName))
	sb.WriteString(fmt.Sprintf("3. After edits, call `%s` to run the configured tests and study both stdout and stderr.\n", runTestsToolName))
	sb.WriteString(fmt.Sprintf("4. Finish only by calling `%s` with status `success` once the instruction is satisfied. If you cannot complete the task, call it with status `failure` and explain why.\n", finalizeToolName))
	if strings.TrimSpace(r.Test) != "" {
		sb.WriteString("You must not declare success until the most recent test run after your latest edits exited with code 0.\n")
	}
	sb.WriteString("Reason about the plan before calling tools, cite file paths explicitly, and avoid editing files that were not provided.")
	return sb.String()
}

func buildUserPrompt(instruction, testCmd, projectState string) string {
	instruction = strings.TrimSpace(instruction)
	testCmd = strings.TrimSpace(testCmd)

	var sb strings.Builder
	sb.WriteString("<Task>\n")
	sb.WriteString("  <Instruction>")
	sb.WriteString(toCDATA(instruction))
	sb.WriteString("</Instruction>\n")
	if testCmd != "" {
		sb.WriteString("  <TestCommand>")
		sb.WriteString(toCDATA(testCmd))
		sb.WriteString("</TestCommand>\n")
	}
	sb.WriteString("  <CurrentState>\n")
	sb.WriteString(indentBlock(projectState, "    "))
	sb.WriteString("\n  </CurrentState>\n")
	sb.WriteString("</Task>")
	return sb.String()
}

type getProjectStateTool struct {
	state *runnerState
}

type getProjectStateRequest struct {
	Paths []string `json:"paths,omitempty"`
}

func (t *getProjectStateTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: getProjectStateToolName,
		Desc: "Inspect the current project state, optionally limited to specific file paths.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"paths": {
				Type:     schema.Array,
				Desc:     "Optional relative file paths to include. If omitted the entire project snapshot is returned.",
				ElemInfo: &schema.ParameterInfo{Type: schema.String},
			},
		}),
	}, nil
}

func (t *getProjectStateTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var req getProjectStateRequest
	if strings.TrimSpace(argumentsInJSON) != "" {
		if err := json.Unmarshal([]byte(argumentsInJSON), &req); err != nil {
			return "", fmt.Errorf("get_project_state: parse arguments: %w", err)
		}
	}
	return t.state.renderProjectState(req.Paths), nil
}

type applyEditsTool struct {
	state *runnerState
}

type applyEditsRequest struct {
	Updates []fileUpdate `json:"updates"`
	Summary string       `json:"summary,omitempty"`
}

type fileUpdate struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (t *applyEditsTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: applyEditsToolName,
		Desc: "Write updated file contents. Always send the complete contents for each file you touch.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"updates": {
				Type:     schema.Array,
				Required: true,
				Desc:     "One or more file updates to apply.",
				ElemInfo: &schema.ParameterInfo{
					Type: schema.Object,
					SubParams: map[string]*schema.ParameterInfo{
						"path": {
							Type:     schema.String,
							Required: true,
							Desc:     "Relative file path from the project root.",
						},
						"content": {
							Type:     schema.String,
							Required: true,
							Desc:     "Full file contents after your edit.",
						},
					},
				},
			},
			"summary": {
				Type: schema.String,
				Desc: "Optional short summary of the edits.",
			},
		}),
	}, nil
}

func (t *applyEditsTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	if strings.TrimSpace(argumentsInJSON) == "" {
		return "", errors.New("apply_edits: missing arguments")
	}
	var req applyEditsRequest
	if err := json.Unmarshal([]byte(argumentsInJSON), &req); err != nil {
		return "", fmt.Errorf("apply_edits: parse arguments: %w", err)
	}
	if len(req.Updates) == 0 {
		return "", errors.New("apply_edits: provide at least one file in updates")
	}

	appliedPaths, err := t.state.applyUpdates(req.Updates)
	if err != nil {
		return "", err
	}

	summary := strings.TrimSpace(req.Summary)
	if summary == "" {
		summary = fmt.Sprintf("Applied %d file update(s).", len(appliedPaths))
	}

	filesSnapshot := t.state.renderFiles(appliedPaths)
	return fmt.Sprintf("%s\n%s", summary, filesSnapshot), nil
}

type runTestsTool struct {
	state *runnerState
}

type runTestsRequest struct {
	Command        string            `json:"command,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
}

func (t *runTestsTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: runTestsToolName,
		Desc: "Execute the configured test command. You may override the command or timeout if necessary.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"command": {
				Type: schema.String,
				Desc: "Optional custom command. Defaults to the runner's configured test command.",
			},
			"timeout_seconds": {
				Type: schema.Integer,
				Desc: "Optional timeout override in seconds.",
			},
			"env": {
				Type: schema.Object,
				Desc: "Optional additional environment variables (key/value).",
			},
		}),
	}, nil
}

func (t *runTestsTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var req runTestsRequest
	if strings.TrimSpace(argumentsInJSON) != "" {
		if err := json.Unmarshal([]byte(argumentsInJSON), &req); err != nil {
			return "", fmt.Errorf("run_tests: parse arguments: %w", err)
		}
	}

	command := strings.TrimSpace(req.Command)
	if command == "" {
		command = t.state.getTestCommand()
	}
	if command == "" {
		return "", errors.New("run_tests: no test command configured")
	}

	timeout := t.state.getTestTimeout()
	if req.TimeoutSeconds > 0 {
		timeout = time.Duration(req.TimeoutSeconds) * time.Second
	}

	outcome, err := t.state.executeTestCommand(ctx, command, timeout, req.Env)
	if err != nil {
		return "", err
	}

	payload := map[string]any{
		"command":           outcome.Command,
		"exit_code":         outcome.ExitCode,
		"duration_ms":       outcome.Duration.Milliseconds(),
		"stdout":            outcome.Stdout,
		"stdout_truncated":  outcome.StdoutTruncated,
		"stderr":            outcome.Stderr,
		"stderr_truncated":  outcome.StderrTruncated,
		"timed_out":         outcome.TimedOut,
		"started_at":        outcome.StartedAt.Format(time.RFC3339),
		"completed_at":      outcome.CompletedAt.Format(time.RFC3339),
		"working_directory": t.state.getWorkdir(),
	}

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", fmt.Errorf("run_tests: marshal result: %w", err)
	}
	return string(data), nil
}

type finalizeTool struct {
	state *runnerState
}

type finalizeRequest struct {
	Status  string `json:"status"`
	Summary string `json:"summary,omitempty"`
}

func (t *finalizeTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: finalizeToolName,
		Desc: "Mark the task as complete. Use status `success` only when the instruction is satisfied and tests pass.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"status": {
				Type:     schema.String,
				Required: true,
				Desc:     "Set to `success` or `failure`.",
				Enum:     []string{"success", "failure"},
			},
			"summary": {
				Type: schema.String,
				Desc: "Short explanation of the outcome.",
			},
		}),
	}, nil
}

func (t *finalizeTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	if strings.TrimSpace(argumentsInJSON) == "" {
		return "", errors.New("finalize_task: missing arguments")
	}
	var req finalizeRequest
	if err := json.Unmarshal([]byte(argumentsInJSON), &req); err != nil {
		return "", fmt.Errorf("finalize_task: parse arguments: %w", err)
	}

	status := strings.ToLower(strings.TrimSpace(req.Status))
	if status != "success" && status != "failure" {
		return "", errors.New("finalize_task: status must be \"success\" or \"failure\"")
	}

	if status == "success" && t.state.requiresSuccessfulTests() {
		last := t.state.getLastTestOutcome()
		if !last.Ran {
			return "", errors.New("finalize_task: run tests before reporting success")
		}
		if last.ExitCode != 0 {
			return "", fmt.Errorf("finalize_task: cannot report success while last test exit code is %d", last.ExitCode)
		}
	}

	summary := strings.TrimSpace(req.Summary)
	if summary == "" {
		if status == "success" {
			summary = "Task marked as success."
		} else {
			summary = "Task marked as failure."
		}
	}

	t.state.setFinished(status, summary)

	if err := react.SetReturnDirectly(ctx); err != nil {
		return "", fmt.Errorf("finalize_task: %w", err)
	}

	return summary, nil
}

func (s *runnerState) renderProjectState(paths []string) string {
	s.mu.RLock()
	filesCopy := copyStringMap(s.files)
	testCmd := s.testCmd
	var lastCopy *testOutcome
	if s.lastTest != nil {
		tmp := *s.lastTest
		lastCopy = &tmp
	}
	s.mu.RUnlock()

	var sb strings.Builder
	sb.WriteString("<ProjectState>")
	if testCmd != "" {
		sb.WriteString("\n  <TestCommand>")
		sb.WriteString(toCDATA(testCmd))
		sb.WriteString("</TestCommand>")
	}
	sb.WriteString("\n")
	sb.WriteString(indentBlock(renderFilesXML(filesCopy, paths), "  "))
	sb.WriteString("\n")
	sb.WriteString(indentBlock(renderLastTestXML(lastCopy), "  "))
	sb.WriteString("\n</ProjectState>")
	return sb.String()
}

func (s *runnerState) renderFiles(paths []string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return renderFilesXML(s.files, paths)
}

func (s *runnerState) applyUpdates(updates []fileUpdate) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	applied := make([]string, 0, len(updates))
	for _, upd := range updates {
		path, err := sanitizeRelativePath(upd.Path)
		if err != nil {
			return nil, fmt.Errorf("apply_edits: %w", err)
		}
		full := filepath.Join(s.workdir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return nil, fmt.Errorf("apply_edits: create dir for %s: %w", path, err)
		}

		mode := os.FileMode(0o644)
		if info, err := os.Stat(full); err == nil {
			mode = info.Mode()
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("apply_edits: stat %s: %w", path, err)
		}

		if err := os.WriteFile(full, []byte(upd.Content), mode); err != nil {
			return nil, fmt.Errorf("apply_edits: write %s: %w", path, err)
		}
		s.files[path] = upd.Content
		applied = append(applied, path)
	}

	sort.Strings(applied)
	s.lastTest = nil // invalidate test result after edits
	return applied, nil
}

func (s *runnerState) executeTestCommand(ctx context.Context, command string, timeout time.Duration, extraEnv map[string]string) (testOutcome, error) {
	if timeout <= 0 {
		timeout = defaultTestTimeout
	}

	s.mu.RLock()
	workdir := s.workdir
	s.mu.RUnlock()

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "bash", "-lc", command)
	cmd.Dir = workdir
	cmd.Env = append(os.Environ(), flattenEnv(extraEnv)...)

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	timedOut := errors.Is(runCtx.Err(), context.DeadlineExceeded)
	exitCode := 0

	var exitErr *exec.ExitError
	if err != nil {
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else if timedOut {
			exitCode = -1
		} else {
			exitCode = 1
		}
	}

	stdout := stdoutBuf.String()
	stderr := stderrBuf.String()

	if err != nil && exitErr == nil && !timedOut {
		if stderr != "" && !strings.HasSuffix(stderr, "\n") {
			stderr += "\n"
		}
		stderr += "command error: " + err.Error()
	}

	clippedStdout, stdoutTrunc := clipString(stdout, maxTestOutputRunes)
	clippedStderr, stderrTrunc := clipString(stderr, maxTestOutputRunes)

	outcome := testOutcome{
		Ran:             true,
		Command:         command,
		ExitCode:        exitCode,
		Duration:        duration,
		Stdout:          clippedStdout,
		Stderr:          clippedStderr,
		StdoutTruncated: stdoutTrunc,
		StderrTruncated: stderrTrunc,
		TimedOut:        timedOut,
		StartedAt:       start,
		CompletedAt:     start.Add(duration),
	}

	s.recordTestResult(outcome)
	return outcome, nil
}

func (s *runnerState) recordTestResult(outcome testOutcome) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := outcome
	s.lastTest = &result
}

func (s *runnerState) getLastTestOutcome() testOutcome {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lastTest == nil {
		return testOutcome{}
	}
	return *s.lastTest
}

func (s *runnerState) getTestCommand() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.testCmd
}

func (s *runnerState) getTestTimeout() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.testTimeout
}

func (s *runnerState) getWorkdir() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.workdir
}

func (s *runnerState) requiresSuccessfulTests() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return strings.TrimSpace(s.testCmd) != ""
}

func (s *runnerState) setFinished(status, summary string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finished = true
	s.finishStatus = status
	s.finishSummary = summary
}

func (s *runnerState) validateCompletion() error {
	s.mu.RLock()
	finished := s.finished
	status := s.finishStatus
	summary := s.finishSummary
	testCmd := s.testCmd
	var lastCopy *testOutcome
	if s.lastTest != nil {
		tmp := *s.lastTest
		lastCopy = &tmp
	}
	s.mu.RUnlock()

	if !finished {
		return errors.New("axe: agent stopped without calling finalize_task")
	}
	if status != "success" {
		if summary != "" {
			return fmt.Errorf("axe: agent reported failure: %s", summary)
		}
		return errors.New("axe: agent reported failure")
	}
	if strings.TrimSpace(testCmd) != "" {
		if lastCopy == nil || !lastCopy.Ran {
			return errors.New("axe: finalize_task succeeded without running tests")
		}
		if lastCopy.ExitCode != 0 {
			return fmt.Errorf("axe: finalize_task called with failing tests (exit code %d)", lastCopy.ExitCode)
		}
	}
	return nil
}

func (s *runnerState) snapshotFiles() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return copyStringMap(s.files)
}

func sanitizeRelativePath(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", errors.New("empty path")
	}
	clean := filepath.Clean(trimmed)
	if filepath.IsAbs(clean) {
		return "", fmt.Errorf("absolute path not allowed: %s", trimmed)
	}
	if clean == "." || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("path escapes project root: %s", trimmed)
	}
	return clean, nil
}

func renderFilesXML(files map[string]string, filter []string) string {
	selected := make([]string, 0, len(files))
	if len(filter) == 0 {
		for path := range files {
			selected = append(selected, path)
		}
	} else {
		seen := make(map[string]struct{}, len(filter))
		for _, raw := range filter {
			canon, err := sanitizeRelativePath(raw)
			if err != nil {
				continue
			}
			if _, ok := files[canon]; !ok {
				continue
			}
			if _, exists := seen[canon]; exists {
				continue
			}
			seen[canon] = struct{}{}
			selected = append(selected, canon)
		}
	}
	sort.Strings(selected)

	var sb strings.Builder
	sb.WriteString("<Files>")
	if len(selected) > 0 {
		for _, path := range selected {
			sb.WriteString("\n  <File path=\"")
			sb.WriteString(escapeXMLAttr(path))
			sb.WriteString("\">")
			sb.WriteString(toCDATA(files[path]))
			sb.WriteString("</File>")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("</Files>")
	return sb.String()
}

func renderLastTestXML(outcome *testOutcome) string {
	if outcome == nil || !outcome.Ran {
		return "<LastTest ran=\"false\" />"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(
		"<LastTest ran=\"true\" exit_code=\"%d\" duration_ms=\"%d\" timed_out=\"%t\" started_at=\"%s\" completed_at=\"%s\">",
		outcome.ExitCode,
		outcome.Duration.Milliseconds(),
		outcome.TimedOut,
		outcome.StartedAt.Format(time.RFC3339),
		outcome.CompletedAt.Format(time.RFC3339),
	))
	sb.WriteString("\n  <Command>")
	sb.WriteString(toCDATA(outcome.Command))
	sb.WriteString("</Command>")
	sb.WriteString("\n  <Stdout truncated=\"")
	sb.WriteString(fmt.Sprintf("%t", outcome.StdoutTruncated))
	sb.WriteString("\">")
	sb.WriteString(toCDATA(outcome.Stdout))
	sb.WriteString("</Stdout>")
	sb.WriteString("\n  <Stderr truncated=\"")
	sb.WriteString(fmt.Sprintf("%t", outcome.StderrTruncated))
	sb.WriteString("\">")
	sb.WriteString(toCDATA(outcome.Stderr))
	sb.WriteString("</Stderr>\n</LastTest>")
	return sb.String()
}

func indentBlock(block, indent string) string {
	if block == "" {
		return ""
	}
	lines := strings.Split(block, "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		lines[i] = indent + line
	}
	return strings.Join(lines, "\n")
}

func toCDATA(content string) string {
	escaped := strings.ReplaceAll(content, "]]>", "]]]]><![CDATA[>")
	return "<![CDATA[" + escaped + "]]" + ">"
}

func escapeXMLAttr(value string) string {
	return xmlAttrEscaper.Replace(value)
}

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
		out = append(out, fmt.Sprintf("%s=%s", k, env[k]))
	}
	return out
}

func copyStringMap(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func MustLoadFiles(pattern string) map[string]string {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		panic("axe: empty pattern for MustLoadFiles")
	}

	candidates := []string{pattern}
	if !filepath.IsAbs(pattern) {
		if callerPattern := callerRelativePattern(pattern); callerPattern != "" {
			candidates = append(candidates, callerPattern)
		}
	}

	matchSet := make(map[string]struct{})
	for _, p := range candidates {
		matches, err := filepathx.Glob(p)
		if err != nil {
			panic(fmt.Errorf("axe: glob %q: %w", p, err))
		}
		for _, m := range matches {
			matchSet[m] = struct{}{}
		}
	}

	if len(matchSet) == 0 {
		panic(fmt.Sprintf("axe: no files matched pattern %q", pattern))
	}

	wd, err := os.Getwd()
	if err != nil {
		panic(fmt.Errorf("axe: determine working directory: %w", err))
	}

	files := make(map[string]string)
	var paths []string
	for path := range matchSet {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			panic(fmt.Errorf("axe: stat %s: %w", path, err))
		}
		if info.IsDir() {
			if err := filepath.WalkDir(path, func(p string, d os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if d.IsDir() {
					return nil
				}
				return includeFile(p, wd, files)
			}); err != nil {
				panic(fmt.Errorf("axe: walk %s: %w", path, err))
			}
			continue
		}
		if err := includeFile(path, wd, files); err != nil {
			panic(err)
		}
	}

	if len(files) == 0 {
		panic(fmt.Sprintf("axe: pattern %q matched no regular files", pattern))
	}
	return files
}

func callerRelativePattern(pattern string) string {
	for depth := 2; depth <= 6; depth++ {
		if _, file, _, ok := runtime.Caller(depth); ok && file != "" {
			if strings.HasSuffix(file, "axe.go") {
				continue
			}
			return filepath.Join(filepath.Dir(file), pattern)
		}
	}
	return ""
}

func includeFile(path, base string, out map[string]string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("axe: read %s: %w", path, err)
	}
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return fmt.Errorf("axe: derive relative path for %s: %w", path, err)
	}
	rel = filepath.Clean(rel)
	if strings.HasPrefix(rel, "..") {
		return fmt.Errorf("axe: file %s escapes working directory", path)
	}
	canon, err := sanitizeRelativePath(rel)
	if err != nil {
		return fmt.Errorf("axe: normalize path %s: %w", rel, err)
	}
	if _, exists := out[canon]; exists {
		return nil
	}
	out[canon] = string(data)
	return nil
}
