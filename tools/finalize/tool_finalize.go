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
	StatusSuccess = "success"
	StatusFailure = "failure"
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
	TODO      string `json:"todo,omitempty"`
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
			"todo": {
				Type: schema.String,
				Desc: "Tasks that are not yet implemented, comparing to the original instruction. Make sure to cover all the remaining tasks.",
			},
		}),
	}, nil
}

func (t *FinalizeTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	log.Debug().Msgf("finalize_task: finalizing task: %s", argumentsInJSON)
	if strings.TrimSpace(argumentsInJSON) == "" {
		return "", errors.New("finalize_task: missing arguments")
	}
	var req FinalizeRequest
	if err := json.Unmarshal([]byte(argumentsInJSON), &req); err != nil {
		return "", fmt.Errorf("finalize_task: parse arguments: %w", err)
	}

	status := strings.ToLower(strings.TrimSpace(req.Status))
	if status != StatusSuccess && status != StatusFailure {
		return "", errors.New("finalize_task: status must be \"success\" or \"failure\"")
	}

	summary := strings.TrimSpace(req.Changelog)
	if summary == "" {
		if status == StatusSuccess {
			summary = "Task marked as success."
		} else {
			summary = "Task marked as failure."
		}
	}

	// update changelog
	if t.Changelog != nil {
		t.Changelog.Success = status == StatusSuccess
		t.Changelog.AddLog(summary)
		t.Changelog.TODO = req.TODO
	}

	if err := react.SetReturnDirectly(ctx); err != nil {
		return "", fmt.Errorf("finalize_task: %w", err)
	}

	return summary, nil
}
