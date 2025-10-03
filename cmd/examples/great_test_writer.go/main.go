package main

import (
	"github.com/stumble/axe"
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
	runner := axe.Runner{
		Instruction: instruction,
		Files:       axe.MustLoadFiles("demo/*"),
		Test:        "go test -v",
		Model:       "gpt-4o",
	}
	runner.Run()
}
