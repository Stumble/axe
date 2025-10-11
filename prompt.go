package axe

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/components/prompt"
	"github.com/cloudwego/eino/schema"
	"github.com/stumble/axe/tools/code"
	"github.com/stumble/axe/tools/finalize"
)

func buildInitialMessages(ctx context.Context, r *Runner, codeInputXML string) ([]*schema.Message, error) {
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
