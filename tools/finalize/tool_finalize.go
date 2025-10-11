package finalize

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
	"github.com/rs/zerolog/log"
	"github.com/stumble/axe/history"
)

const (
	FinalizeToolName = "finalize_task"
)

type FinalizeTool struct {
	Changelog *history.Changelog
}

type FinalizeRequest struct {
	Status    string `json:"status"`
	Changelog string `json:"changelog,omitempty"`
}

func (t *FinalizeTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: FinalizeToolName,
		Desc: "Mark the task as complete. Use status `success` only when the instruction is satisfied.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"status": {
				Type:     schema.String,
				Required: true,
				Desc:     "Set to `success` or `failure`.",
				Enum:     []string{"success", "failure"},
			},
			"changelog": {
				Type: schema.String,
				Desc: "A detailed changelog of the task, covering all the changes made to the code, tests.",
			},
		}),
	}, nil
}

func (t *FinalizeTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	log.Info().Msgf("finalize_task: finalizing task: %s", argumentsInJSON)
	if strings.TrimSpace(argumentsInJSON) == "" {
		return "", errors.New("finalize_task: missing arguments")
	}
	var req FinalizeRequest
	if err := json.Unmarshal([]byte(argumentsInJSON), &req); err != nil {
		return "", fmt.Errorf("finalize_task: parse arguments: %w", err)
	}

	status := strings.ToLower(strings.TrimSpace(req.Status))
	if status != "success" && status != "failure" {
		return "", errors.New("finalize_task: status must be \"success\" or \"failure\"")
	}

	summary := strings.TrimSpace(req.Changelog)
	if summary == "" {
		if status == "success" {
			summary = "Task marked as success."
		} else {
			summary = "Task marked as failure."
		}
	}

	// update changelog
	t.Changelog.Success = status == "success"
	t.Changelog.Logs = append(t.Changelog.Logs, summary)

	if err := react.SetReturnDirectly(ctx); err != nil {
		return "", fmt.Errorf("finalize_task: %w", err)
	}

	return summary, nil
}
