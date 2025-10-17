package main

import (
	"context"
	"log"
	"os"

	"github.com/rs/zerolog"

	"github.com/stumble/axe"
	cc "github.com/stumble/axe/code/container"
	clitool "github.com/stumble/axe/tools/cli"
)

var instruction = `
You orchestrate a bug-fix for the Go files in this workspace. You simply delegate the task to Codex (a super smart coding assistant) to fix the bug. After codex fixes the bug, you run the tests to see if the bug is fixed. If the bug is not fixed, you delegate the task to Codex again to fix the bug. You keep doing this until the bug is fixed.

`

func main() {
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	baseDir := "demo" // relative to current working directory
	runner, err := axe.NewRunner(
		baseDir,
		[]string{instruction},
		cc.MustNewCodeContainerFromFS(baseDir, []string{"calc.go", "calc_test.go"}),
		axe.WithTools([]clitool.Definition{
			clitool.MustNewDefinition(
				"go_test",
				"go test ./...",
				"run Go tests in the selected working directory",
				nil,
			),
			clitool.MustNewDefinition(
				"use_codex",
				"codex e --skip-git-repo-check ",
				"ask Codex (a super smart coding assistant) to edit the code to fix the bug. You must provide one argument to the command, which is the instruction/prompt/requirement to fix the bug. You must run it in the correct working directory.",
				nil,
			),
		}),
		axe.WithModel(axe.ModelGPT4Dot1),
		axe.WithSink(os.Stdout),
	)
	if err != nil {
		log.Fatalf("failed to create runner: %v", err)
	}
	if err = runner.Run(context.Background(), true); err != nil {
		log.Fatalf("failed to run: %v", err)
	}
}
