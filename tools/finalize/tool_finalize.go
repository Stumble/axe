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
)

const (
	FinalizeToolName = "finalize_task"
)

type FinalizeTool struct{}

type FinalizeRequest struct {
	Status  string `json:"status"`
	Summary string `json:"summary,omitempty"`
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
			"summary": {
				Type: schema.String,
				Desc: "Short explanation of the outcome.",
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

	summary := strings.TrimSpace(req.Summary)
	if summary == "" {
		if status == "success" {
			summary = "Task marked as success."
		} else {
			summary = "Task marked as failure."
		}
	}

	if err := react.SetReturnDirectly(ctx); err != nil {
		return "", fmt.Errorf("finalize_task: %w", err)
	}

	return summary, nil
}
