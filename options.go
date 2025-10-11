package axe

import (
	"path/filepath"
	"time"

	"github.com/stumble/axe/history"
	clitool "github.com/stumble/axe/tools/cli"
)

type RunnerOption func(*Runner) error

func WithModel(model ModelName) RunnerOption {
	return func(r *Runner) error {
		r.Model = model
		return nil
	}
}

func WithMaxSteps(maxSteps int) RunnerOption {
	return func(r *Runner) error {
		r.MaxSteps = maxSteps
		return nil
	}
}

func WithTools(tools []clitool.Definition) RunnerOption {
	return func(r *Runner) error {
		r.Tools = tools
		return nil
	}
}

func WithHistory(historyFilePath string) RunnerOption {
	return func(r *Runner) error {
		var err error
		r.History, err = history.ReadHistoryFromFile(historyFilePath)
		return err
	}
}

func WithMinInterval(minInterval time.Duration) RunnerOption {
	return func(r *Runner) error {
		r.MinInterval = minInterval
		return nil
	}
}

func (r *Runner) applyDefaults() {
	if r.History == nil {
		r.History = &history.History{FilePath: filepath.Join(r.BaseDir, DefaultHistoryFile)}
	}
	if r.MaxSteps <= 0 {
		r.MaxSteps = defaultMaxSteps
	}
	if r.Model == "" {
		r.Model = ModelGPT4o
	}
}
