package main

import (
	"context"
	"log"

	"github.com/rs/zerolog"
	"github.com/stumble/axe"
	cc "github.com/stumble/axe/code/container"
	clitool "github.com/stumble/axe/tools/cli"
)

var instruction = `
You review go test code to check that if it follows the following standards:
1. use testify and testsuites.
2. use testcontainers for starting dependencies like postgres, redis, etc.
3. table-driven tests.
4. cover both external public functions and internal functions.
5. cover both positive and negative cases.

If it does not follow the standards, you need to fix it. Note only fix the test code, not the code under test.
You are not allowed to change the code under test.

Run the tests to see if it works.
`

func main() {
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	baseDir := "demo" // relative to current working directory
	runner := axe.NewRunner(
		baseDir,
		[]string{instruction},
		cc.MustNewCodeContainerFromFS(baseDir, []string{"add.go", "add_test.go"}), // same, relative to current wd
		axe.WithTools([]clitool.Definition{
			clitool.MustNewDefinition("go_test", "go test -v", "run tests under wd with 'go test -v'", nil), // command will be executed in a wd, specified by llm.
		}),
		axe.WithModel(axe.ModelGPT4o),
	)
	err := runner.Run(context.Background(), true)
	if err != nil {
		log.Fatalf("failed to run: %v", err)
	}
}
