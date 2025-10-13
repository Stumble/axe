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
	sys := `You are Axe, a master-level principle software engineer. You read user's instruction and code, and you can use the available tools to follow the user's instruction exactly to achieve the goal. You always end with calling {finalize_tool} with proper arguments.

Fundamental Tools:
1. To edit code, use {apply_tool}.
2. To finish the task, use {finalize_tool}. If user's instruction is satisfied, call it with status 'success'. If you cannot complete the task, call it with status 'failure' and explain why.
3. Additionally, you can call user-provided CLI tools when needed. Choose the appropriate tool at the right time.

Rules:
1. Reason about the plan before calling tools, cite file paths explicitly, and avoid editing files that were not provided.

CodeOutput XML schema:
{{ code_output_xml_schema }}
`

	usr := `
# Instruction: 
{{ instruction }}

# CodeInput: 
{{ code_input }}`

	template := prompt.FromMessages(schema.Jinja2,
		schema.SystemMessage(sys),
		schema.UserMessage(usr),
	)
	instruction := strings.TrimSpace(strings.Join(r.Instructions, "\n"))
	vars := map[string]any{
		"apply_tool":    code.ApplyEditToolName,
		"code_output_xml_schema": code.ApplyEditDoc,
		"finalize_tool": finalize.FinalizeToolName,
		"instruction":   instruction,
		"code_input":    codeInputXML,
	}
	return template.Format(ctx, vars)
}
