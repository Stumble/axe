# Axe

Axe is a meta ai-coding sdk. It is built for this specific scenario:

1. You know exactly what context should be needs to be send to LLM.
2. You know exactly which code needs to be read and write by LLM.
3. You want to do some structured tasks (e.g. repetitive refactoring for many files) that those coding agents sometimes just can't do it right.

Here's how you can do it with Axe:

1. import axe sdk.
2. programmatically create a runner for each task, .e.g, use code to express your structured task.
3. run the runner.


for (dir of dirs) {
  run({
    instruction: "xxx",
    files: map[string]string,
    test: "" // cmd to run tests after edits.
  }) // this will be a reactive loop that until the test result and the code follows the instruction.
}

This is a sdk, user needs to write their own script to actually make it work.
It is a workflow, specifically for cases you don't want too "agentic" behaviors.
