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
`

func main() {
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	runner := axe.NewRunner(
		"demo/.axe_history.xml",
		[]string{instruction},
		cc.MustNewCodeContainerFromFS("demo", []string{"add.go", "add_test.go"}),
		axe.WithTools([]clitool.Definition{
			{
				Name:    "run_tests",
				Desc:    "Run the tests",
				Command: "go test -v",
			},
		}),
		axe.WithModel(axe.ModelGPT4o),
	)
	err := runner.Run(context.Background(), true)
	if err != nil {
		log.Fatalf("failed to run: %v", err)
	}
}
