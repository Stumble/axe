package code

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/rs/zerolog/log"

	cont "github.com/stumble/axe/code/container"
)

const (
	// ApplyEditToolName is the public name exposed to the agent for applying code edits.
	ApplyEditToolName = "apply_edit"
)

//go:embed apply_edit.md
var ApplyEditDoc string

// ApplyEditTool applies a CodeOutput XML to an in-memory CodeContainer and persists changes to disk.
//
// The tool expects JSON arguments with a single required field:
//   - code_output: XML string representing cont.CodeOutput edits (see comments on cont.CodeOutput)
//
// It returns a short summary string indicating which files were written.
type ApplyEditTool struct {
	Code *cont.CodeContainer
}

type ApplyEditRequest struct {
	CodeOutput string `json:"code_output"`
}

// Info implements the tool metadata for exposure to the agent runtime.
func (t *ApplyEditTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: ApplyEditToolName,
		Desc: "Apply your code edits with <CodeOutput> XML format. Must follow the <CodeOutput> XML schema.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"code_output": {
				Type:     schema.String,
				Required: true,
				Desc:     "XML string of CodeOutput edits to apply.",
			},
		}),
	}, nil
}

// InvokableRun applies the provided CodeOutput XML to the container and writes changed files to disk.
// Invokable don't return error unless it is unrecoverable. It just returns the error as message to the model and let
// the model to handle it.
func (t *ApplyEditTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	log.Debug().Msgf("apply_edit: applying edits: %s", argumentsInJSON)
	if strings.TrimSpace(argumentsInJSON) == "" {
		return "apply_edit: missing arguments, empty string", nil
	}
	if t == nil || t.Code == nil {
		// fatal
		return "", errors.New("apply_edit: tool not initialized with a CodeContainer")
	}

	var req ApplyEditRequest
	if err := json.Unmarshal([]byte(argumentsInJSON), &req); err != nil {
		return fmt.Sprintf("apply_edit: failed to parse arguments: %v", err), nil
	}

	xmlPayload := strings.TrimSpace(req.CodeOutput)
	if xmlPayload == "" {
		return "apply_edit: xml payload is empty", nil
	}

	co, err := cont.ParseCodeOutput(xmlPayload)
	if err != nil {
		return fmt.Sprintf("apply_edit: failed to parse CodeOutput XML: %v", err), nil
	}

	changed, err := t.Code.Apply(co)
	if err != nil {
		return fmt.Sprintf("apply_edit: failed to apply edits: %v", err), nil
	}

	if len(changed) == 0 {
		return "apply_edit: no changes to apply", nil
	}

	log.Debug().Msgf("apply_edit: changed files: %s", strings.Join(changed, ", "))

	// Persist only the changed files. Empty baseDir writes paths as-is (absolute or relative).
	written, err := t.Code.WriteToFiles(changed)
	if err != nil {
		return fmt.Sprintf("failed to write files: %v", err), nil
	}

	log.Debug().Msgf("apply_edit: written files: %s", strings.Join(written, ", "))

	// Build a concise summary
	return fmt.Sprintf("Applied edits to %d file(s): %s", len(written), strings.Join(written, ", ")), nil
}
