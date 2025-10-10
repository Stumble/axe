package clitool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubprocessExecutor_Success(t *testing.T) {
	ctx := context.Background()
	exec := &SubprocessExecutor{}
	argv := []string{"/bin/sh", "-c", "printf %s hello"}
	out, err := exec.Execute(ctx, argv, nil, "")
	require.NoError(t, err)
	assert.True(t, out.Ran)
	assert.Equal(t, 0, out.ExitCode)
	assert.Equal(t, "hello", out.Stdout)
	assert.Equal(t, "", out.Stderr)
}

func TestSubprocessExecutor_NonZeroExit(t *testing.T) {
	ctx := context.Background()
	exec := &SubprocessExecutor{}
	argv := []string{"/bin/sh", "-c", "exit 7"}
	out, err := exec.Execute(ctx, argv, nil, "")
	require.NoError(t, err)
	assert.True(t, out.Ran)
	assert.Equal(t, 7, out.ExitCode)
}

func TestSubprocessExecutor_Timeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	exec := &SubprocessExecutor{}
	argv := []string{"/bin/sh", "-c", "sleep 2"}
	out, err := exec.Execute(ctx, argv, nil, "")
	require.NoError(t, err)
	assert.True(t, out.Ran)
	assert.Equal(t, -1, out.ExitCode)
}

func TestSubprocessExecutor_EnvPropagation(t *testing.T) {
	ctx := context.Background()
	exec := &SubprocessExecutor{}
	argv := []string{"/bin/sh", "-c", "printf %s \"$FOO\""}
	env := map[string]string{"FOO": "bar"}
	out, err := exec.Execute(ctx, argv, env, "")
	require.NoError(t, err)
	assert.Equal(t, "bar", out.Stdout)
}

func TestSubprocessExecutor_Workdir(t *testing.T) {
	ctx := context.Background()
	exec := &SubprocessExecutor{}
	dir := t.TempDir()
	argv := []string{"/bin/sh", "-c", "pwd"}
	out, err := exec.Execute(ctx, argv, nil, dir)
	require.NoError(t, err)
	got := strings.TrimSpace(out.Stdout)
	// Some systems resolve symlinks; compare basenames when necessary
	assert.True(t, got == dir || filepath.Base(got) == filepath.Base(dir), "pwd=%q dir=%q", got, dir)
}

func TestSubprocessExecutor_TruncationStdout(t *testing.T) {
	ctx := context.Background()
	exec := &SubprocessExecutor{}
	// Generate 4001 bytes using yes/head to force clipping at 3000 runes
	argv := []string{"/bin/sh", "-c", "yes A | head -c 4001"}
	out, err := exec.Execute(ctx, argv, nil, "")
	require.NoError(t, err)
	assert.Contains(t, out.Stdout, truncatedMarker)
	// Ensure both prefix and suffix exist around the marker
	parts := strings.Split(out.Stdout, truncatedMarker)
	require.Len(t, parts, 2)
	assert.Greater(t, len(parts[0]), 0)
	assert.Greater(t, len(parts[1]), 0)
}

func TestDefinition_EnvPrecedence(t *testing.T) {
	ctx := context.Background()
	// With current semantics, Definition env overrides inline assignments
	def := MustNewDefinition(
		"echoFoo",
		"FOO=inline /bin/sh -c 'printf %s \"$FOO\"'",
		"",
		map[string]string{"FOO": "base"},
	)
	exec := &SubprocessExecutor{}
	out, err := exec.Execute(ctx, def.Args, def.Env, "")
	require.NoError(t, err)
	assert.Equal(t, "base", out.Stdout)
}

// Removed OutcomeJSON-level tests; InvokableRun returns the Outcome JSON directly

func TestHelpers_parseEnvKVs(t *testing.T) {
	got := parseEnvKVs([]string{" FOO=bar ", "BAZ=qux", "NOEQ", "=empty", "X=1=2"})
	// "NOEQ" and "=empty" are skipped; "X=1=2" keeps everything after first '=' in value
	assert.Equal(t, map[string]string{"FOO": "bar", "BAZ": "qux", "X": "1=2"}, got)
}

func TestHelpers_flattenEnv(t *testing.T) {
	m := map[string]string{"B": "2", "A": "1"}
	flat := flattenEnv(m)
	// Sorted by key
	assert.Equal(t, []string{"A=1", "B=2"}, flat)
}

func TestHelpers_MergeEnv(t *testing.T) {
	base := map[string]string{"A": "1", "B": "2"}
	over := map[string]string{"B": "20", "C": "3"}
	merged := MergeEnv(base, over)
	assert.Equal(t, map[string]string{"A": "1", "B": "20", "C": "3"}, merged)
	// Ensure original maps not mutated
	assert.Equal(t, map[string]string{"A": "1", "B": "2"}, base)
	assert.Equal(t, map[string]string{"B": "20", "C": "3"}, over)
}

func TestHelpers_clipString(t *testing.T) {
	// No clip when under limit
	s, clipped := clipString("hello", 10)
	assert.Equal(t, "hello", s)
	assert.False(t, clipped)
	// Clip when over limit; marker present and halves around it
	big := strings.Repeat("A", 101)
	s, clipped = clipString(big, 50)
	assert.True(t, clipped)
	assert.Contains(t, s, truncatedMarker)
	parts := strings.Split(s, truncatedMarker)
	require.Len(t, parts, 2)
	assert.Equal(t, 25, len([]rune(parts[0])))
	assert.Equal(t, 25, len([]rune(parts[1])))
	// Edge: limit <= 0 returns input
	s, clipped = clipString("abc", 0)
	assert.Equal(t, "abc", s)
	assert.False(t, clipped)
}

func TestHelpers_justClipString(t *testing.T) {
	big := strings.Repeat("B", 1000)
	s := justClipString(big, 100)
	assert.Contains(t, s, truncatedMarker)
}

func TestCliTool_Info_DefaultsAndArgsParam(t *testing.T) {
	tool := &CliTool{Def: MustNewDefinition("cli_tool", "/bin/echo", "Run a configured CLI command", nil)}
	info, err := tool.Info(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "cli_tool", info.Name)
	assert.Contains(t, info.Desc, "Run a configured CLI command")
	// Ensure params-oneOf exists and includes args definition
	require.NotNil(t, info.ParamsOneOf)
	// The exact structure of ParamsOneOf is internal; sanity-check by round-tripping schema
	params := map[string]*schema.ParameterInfo{
		"args": {Type: schema.Array, ElemInfo: &schema.ParameterInfo{Type: schema.String}},
	}
	alt := schema.NewParamsOneOfByParams(params)
	require.NotNil(t, alt)
}

func TestCliTool_InvokableRun_SuccessArgsAndEnvPrecedence(t *testing.T) {
	ctx := context.Background()
	tool := &CliTool{Def: MustNewDefinition(
		"mycli",
		"FOO=inline /bin/sh -c 'printf %s \"$FOO:$0\"'",
		"",
		map[string]string{"FOO": "base"},
	)}
	dir := t.TempDir()
	args := map[string]any{"args": []string{"ARGVAL"}, "workdir": dir}
	data, _ := json.Marshal(args)
	resp, err := tool.InvokableRun(ctx, string(data))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(resp), &m))
	// Definition env overrides inline assignment; $0 reflects the appended arg to sh -c
	assert.Equal(t, "base:ARGVAL", m["Stdout"])
}

func TestCliTool_InvokableRun_InvalidJSON(t *testing.T) {
	tool := &CliTool{Def: MustNewDefinition("echo", "/bin/echo", "", nil)}
	resp, err := tool.InvokableRun(context.Background(), "not json")
	require.NoError(t, err)
	assert.Contains(t, resp, "invalid arguments")
}

func TestCliTool_InvokableRun_MissingWorkdir_ReturnsMessage(t *testing.T) {
	ctx := context.Background()
	tool := &CliTool{Def: MustNewDefinition("pwd", "/bin/sh -c pwd", "", nil)}
	// Missing workdir should return a helpful message and no error
	resp, err := tool.InvokableRun(ctx, "{}")
	require.NoError(t, err)
	assert.Contains(t, resp, "workdir is required")
}

func TestCliTool_InvokableRun_TimeoutExitCode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	tool := &CliTool{Def: MustNewDefinition("sleep", "/bin/sh -c 'sleep 2'", "", nil)}
	dir := t.TempDir()
	args := map[string]any{"workdir": dir}
	data, _ := json.Marshal(args)
	resp, err := tool.InvokableRun(ctx, string(data))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(resp), &m))
	// Exit code -1 indicates timeout in our executor
	assert.Equal(t, float64(-1), m["ExitCode"]) // JSON numbers are float64
}

func TestCliTool_InvokableRun_WorkdirUsed(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	tool := &CliTool{Def: MustNewDefinition("pwd", "/bin/sh -c pwd", "", nil)}
	args := map[string]any{"workdir": dir}
	data, _ := json.Marshal(args)
	resp, err := tool.InvokableRun(ctx, string(data))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(resp), &m))
	got := strings.TrimSpace(m["Stdout"].(string))
	assert.True(t, got == dir || filepath.Base(got) == filepath.Base(dir), "pwd=%q dir=%q", got, dir)
}

func TestCliTool_InvokableRun_WorkdirScriptEnvMerge(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "script.sh")
	// Script prints FOO value
	script := "#!/bin/sh\nprintf %s \"$FOO\""
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o755))
	require.NoError(t, os.Chmod(scriptPath, 0o755))

	tool := &CliTool{Def: MustNewDefinition(
		"runscript",
		"/bin/sh -c './script.sh'",
		"",
		map[string]string{"FOO": "base"},
	)}

	args := map[string]any{"workdir": dir}
	data, _ := json.Marshal(args)
	resp, err := tool.InvokableRun(ctx, string(data))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(resp), &m))
	assert.Equal(t, "base", m["Stdout"])
}

// Removed empty argv and nil receiver tests; current implementation assumes non-nil receiver

func TestNewDefinition_CommandParseError(t *testing.T) {
	_, err := NewDefinition("x", "\"unterminated quote", "", nil)
	assert.Error(t, err)
}

// Removed OutcomeJSON tests since OutcomeJSON helper is no longer present

func TestSubprocessExecutor_TruncationStderr(t *testing.T) {
	ctx := context.Background()
	exec := &SubprocessExecutor{}
	// Produce large stderr content and ensure it is truncated
	argv := []string{"/bin/sh", "-c", "yes E | head -c 4001 1>&2"}
	out, err := exec.Execute(ctx, argv, nil, "")
	require.NoError(t, err)
	assert.Contains(t, out.Stderr, truncatedMarker)
	parts := strings.Split(out.Stderr, truncatedMarker)
	require.Len(t, parts, 2)
	assert.Greater(t, len(parts[0]), 0)
	assert.Greater(t, len(parts[1]), 0)
}

func TestSubprocessExecutor_CommandNotFound_ErrorDecoration(t *testing.T) {
	ctx := context.Background()
	exec := &SubprocessExecutor{}
	// Intentionally execute a non-existent absolute path so process fails to start
	argv := []string{"/ definitely-not-a-binary-xyz"}
	out, err := exec.Execute(ctx, argv, nil, "")
	require.NoError(t, err)
	assert.True(t, out.Ran)
	assert.Equal(t, 1, out.ExitCode)
	assert.Contains(t, out.Stderr, "command error:")
}

func TestDefinition_InlineEnvPersistsWhenNotOverridden(t *testing.T) {
	ctx := context.Background()
	def := MustNewDefinition(
		"echoFooInline",
		"FOO=inline /bin/sh -c 'printf %s \"$FOO\"'",
		"",
		nil,
	)
	exec := &SubprocessExecutor{}
	out, err := exec.Execute(ctx, def.Args, def.Env, "")
	require.NoError(t, err)
	assert.Equal(t, "inline", out.Stdout)
}

func TestCliTool_InvokableRun_EmptyArgumentsString(t *testing.T) {
	ctx := context.Background()
	tool := &CliTool{Def: MustNewDefinition("ok", "/bin/sh -c 'printf %s OK'", "", nil)}
	// Empty arguments string becomes "{}"; since workdir is required, expect message
	resp, err := tool.InvokableRun(ctx, "")
	require.NoError(t, err)
	assert.Contains(t, resp, "workdir is required")
}

func TestOutcome_String_Success(t *testing.T) {
	ctx := context.Background()
	exec := &SubprocessExecutor{}
	argv := []string{"/bin/sh", "-c", "printf %s hello"}
	out, err := exec.Execute(ctx, argv, nil, "")
	require.NoError(t, err)
	s := out.String()
	assert.Contains(t, s, "Command:")
	assert.Contains(t, s, "Result: succeeded")
	assert.Contains(t, s, "Duration:")
	assert.Contains(t, s, "Stdout:")
	assert.Contains(t, s, "hello")
}

func TestOutcome_String_NonZero(t *testing.T) {
	ctx := context.Background()
	exec := &SubprocessExecutor{}
	argv := []string{"/bin/sh", "-c", "exit 7"}
	out, err := exec.Execute(ctx, argv, nil, "")
	require.NoError(t, err)
	s := out.String()
	assert.Contains(t, s, "Result: exited with code 7")
	// No stdout section expected; stderr might be empty depending on shell
}

func TestOutcome_String_Timeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	exec := &SubprocessExecutor{}
	argv := []string{"/bin/sh", "-c", "sleep 2"}
	out, err := exec.Execute(ctx, argv, nil, "")
	require.NoError(t, err)
	s := out.String()
	assert.Contains(t, s, "Result: timed out")
}
