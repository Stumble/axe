# Axe

Axe is a meta AI-coding SDK for orchestrating large language models (LLMs) to execute structured software
engineering workflows. Instead of delegating all decisions to an autonomous "agent", you describe the
exact files, instructions, and supporting tools that the model should use. Axe repeatedly drives the LLM
through a tight loop of editing code and running checks until your constraints are satisfied.

This document provides a detailed, end-to-end tutorial for building your first Axe runner, explains the
core concepts, and highlights common extensions that you can incorporate in your own automation scripts.

## Table of contents

1. [Why Axe?](#why-axe)
2. [Prerequisites](#prerequisites)
3. [Installation](#installation)
4. [Core concepts](#core-concepts)
   1. [Runner](#runner)
   2. [Instructions](#instructions)
   3. [Code containers](#code-containers)
   4. [Tools](#tools)
5. [Tutorial: build a test-review assistant](#tutorial-build-a-test-review-assistant)
   1. [Project layout](#project-layout)
   2. [Define quality standards](#define-quality-standards)
   3. [Configure the code container](#configure-the-code-container)
   4. [Expose useful tools](#expose-useful-tools)
   5. [Initialize and run the runner](#initialize-and-run-the-runner)
6. [Running and monitoring](#running-and-monitoring)
7. [Customizing your workflow](#customizing-your-workflow)
8. [Troubleshooting](#troubleshooting)
9. [Next steps](#next-steps)

## Why Axe?

Traditional coding agents decide which files to inspect, what commands to run, and how to satisfy your
requirements. That flexibility can backfire when you already know the precise scope of work or need to
apply the same transformation across many repositories. Axe gives you deterministic control over:

- **Scope** – List the exact files or directories the LLM may modify.
- **Guidance** – Provide structured natural-language or templated instructions for the task at hand.
- **Feedback loops** – Run verifiers (tests, linters, formatters) inside the same automated workflow.
- **Repeatability** – Encode the workflow as Go code, making it trivial to version, review, and extend.

## Prerequisites

Before running the examples in this tutorial, make sure you have:

- [Go](https://go.dev/) **1.24** or newer installed.
- An OpenAI-compatible API key exported as either `OPENAI_API_KEY` or `OAI_MY_KEY`.
- Access to the codebase you want to transform. The example below uses the `demo` directory in this
  repository.

You can optionally create a `.env` file with the API key and load it with your preferred method before
invoking your runner.

## Installation

Add Axe to your Go module by running:

```bash
go get github.com/stumble/axe
```

This makes the `github.com/stumble/axe` package and its helper packages (code containers, CLI tools, etc.)
available for import in your projects.

## Core concepts

### Runner

A **runner** owns the lifecycle of a workflow. You configure it with instructions, a set of files the model
is allowed to touch, and optional hooks such as command-line tools. When you call `runner.Run`, Axe enters a
reactive loop: the model proposes edits, Axe applies them, and then Axe executes any verification commands
(like tests) until all instructions are satisfied or an error occurs.

### Instructions

Instructions are natural-language prompts that tell the model how to modify the code. You can supply one or
multiple instructions, and they will be executed sequentially. Use this to break down complex workflows into
manageable steps.

### Code containers

A code container defines the subset of the file system that the LLM can read or write. Axe ships with file
system containers such as `code/container.MustNewCodeContainerFromFS`, which points at specific files or
directories relative to your working directory.

### Tools

Tools expose commands that the model can invoke. These are especially useful for running tests, formatters,
or custom analyzers without leaving the controlled environment. Axe ships with helper packages like
`tools/cli` for defining shell commands that the model can request.

## Tutorial: build a test-review assistant

The `cmd/examples/great_test_writer/main.go` file in this repository demonstrates how to combine the
concepts above. The following walkthrough rebuilds that example step by step so you can adapt it to your own
projects.

### Project layout

Create the following structure in your workspace (the example below lives in `cmd/examples` within this
repository):

```
my-workflow/
├── go.mod
├── go.sum
├── cmd/
│   └── great_test_writer/
│       └── main.go
└── demo/
    ├── add.go
    └── add_test.go
```

The `demo` directory contains the code you want the LLM to modify. The runner lives in `cmd/great_test_writer/main.go`.

### Define quality standards

Start by declaring the standards that your test suite must satisfy. Axe will feed this text to the LLM:

```go
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
```

You can tailor this string (or provide multiple instructions) to match your organization’s guidelines.

### Configure the code container

Tell Axe where to find the files that the model should read and edit. In this example we keep everything in
`demo`, and we only allow changes to two files:

```go
cc.MustNewCodeContainerFromFS(baseDir, []string{"add.go", "add_test.go"})
```

Here `baseDir` is `"demo"`, relative to the runner’s working directory.

### Expose useful tools

Give the model the ability to run Go tests by registering a CLI tool:

```go
clitool.MustNewDefinition(
    "go_test",
    "go test -v",
    "run tests under wd with 'go test -v'",
    nil, // optional environment overrides
)
```

The `go_test` tool can then be invoked by the LLM whenever it needs to validate its changes.

### Initialize and run the runner

Put everything together inside your `main` function:

```go
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

func main() {
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	baseDir := "demo" // relative to current working directory
	runner, err := axe.NewRunner(
		baseDir,
		[]string{instruction},
		cc.MustNewCodeContainerFromFS(baseDir, []string{"add.go", "add_test.go"}), // same, relative to current wd
		axe.WithTools([]clitool.Definition{
			clitool.MustNewDefinition("go_test", "go test -v", "run tests under wd with 'go test -v'", nil), // command will be executed in a wd, specified by llm.
		}),
		axe.WithModel(axe.ModelGPT4Dot1),
		axe.WithSink(os.Stdout),
	)
	if err != nil {
		log.Fatalf("failed to create runner: %v", err)
	}
	err = runner.Run(context.Background(), true)
	if err != nil {
		log.Fatalf("failed to run: %v", err)
	}
}
```

When executed, the runner creates a feedback loop where the model edits `add_test.go` until the tests pass and
the instruction criteria are satisfied.

## Running and monitoring

1. Export your API key: `export OPENAI_API_KEY=sk-...`.
2. Change to the directory that contains your runner (for example, `cmd/examples/great_test_writer`).
3. Run `go run .` to start the workflow.
4. Watch the logs to follow the LLM’s actions. The runner will log each edit, command invocation, and
   verification result.

If a command fails (for example, `go test` returns a non-zero status), Axe reports the failure back to the
model so it can attempt a fix. The loop continues until Axe determines that the instruction criteria are
satisfied or until an unrecoverable error occurs.

## Customizing your workflow

- **Multiple instructions:** Pass a slice of strings to `axe.NewRunner` to create multi-step workflows.
- **Broader file scopes:** Use other code container constructors (or implement your own) to point at entire
  directories, glob patterns, or virtual filesystems.
- **Additional tools:** Register linters, formatters, build scripts, or even HTTP endpoints that the model can
  call.
- **Different models:** Choose from the models supported in `axe.Model`, or provide a custom implementation if
  you have your own inference endpoint.
- **Non-Go projects:** As long as your tooling can be expressed as CLI commands, Axe can drive workflows for
  any language or framework.

## Troubleshooting

- **Missing API key:** Ensure `OPENAI_API_KEY` or `OAI_MY_KEY` is set before running the program. The SDK will
  return `axe: missing OpenAI API key` otherwise.
- **Permission errors:** Confirm that the files listed in your code container exist and that you have write
  access to them.
- **Long-running commands:** Tools run in the working directory you specify. If they hang, inspect the command
  output and update your instruction or tooling configuration.

## Next steps

- Explore the other examples in `cmd/examples` for more complex workflows.
- Wrap your runner in a CLI or service so teammates can trigger it with their own inputs.
- Combine Axe with scheduling systems or CI pipelines to enforce standards across many repositories.

With Axe, you can codify repeatable workflows and keep your engineering teams in control of how LLMs modify
critical code.
