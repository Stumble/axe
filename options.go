package axe

import (
	"fmt"
	"io"
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
		if err != nil {
			return fmt.Errorf("axe: read history file: %w", err)
		}
		return nil
	}
}

func WithMinInterval(minInterval time.Duration) RunnerOption {
	return func(r *Runner) error {
		r.MinInterval = minInterval
		return nil
	}
}

func WithSink(sink io.Writer) RunnerOption {
	return func(r *Runner) error {
		r.Sink = sink
		return nil
	}
}

func WithOutputBufferSize(bufferSize int) RunnerOption {
	return func(r *Runner) error {
		r.Output = make(chan string, bufferSize)
		return nil
	}
}

func (r *Runner) applyDefaults() error {
	if r.History == nil {
		historyFile := filepath.Join(r.BaseDir, DefaultHistoryFile)
		history, err := history.ReadHistoryFromFile(historyFile)
		if err != nil {
			return fmt.Errorf("axe: read history file: %w", err)
		}
		r.History = history
	}
	if r.MaxSteps <= 0 {
		r.MaxSteps = DefaultMaxSteps
	}
	if r.Model == "" {
		r.Model = ModelGPT4o
	}
	return nil
}
