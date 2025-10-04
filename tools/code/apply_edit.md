### apply_edit — Apply CodeOutput XML edits and persist changed files

Use this tool to apply code edits to the in-memory code container and write only the changed files back to disk.

- **Argument (JSON)**: `{"code_output": "<CodeOutput>...</CodeOutput>"}`
- **Edits Model**: Provide a `CodeOutput` XML with any mix of `Rewrite` (full-file replace) and `ApplyDiff` (patch) entries.

### CodeOutput XML schema

```xml
<CodeOutput version="optional-version">
  <Rewrite path="/abs/or/relative/path.ext"><![CDATA[
<full new file content>
]]></Rewrite>

  <ApplyDiff path="/abs/or/relative/path.ext"><![CDATA[
@@ optional anchor hint text
<context line 1>
- <deleted line>
+ <added line>
<context line 2>
---
@@ another optional anchor
<context>
- <deleted>
+ <added>
]]></ApplyDiff>
</CodeOutput>
```

- **`Rewrite`**: Replaces the entire file content. To clear a file without deleting it, provide an empty CDATA.
- **`ApplyDiff`**: Applies a patch to the current file content.
  - Lines starting with `- ` are deletions, `+ ` are additions, unprefixed lines are context.
  - Optional anchor lines start with `@@` and act as search hints.
  - Separate multiple hunks with a line that is exactly `---`.
  - Whitespace matching is tolerant in stages (exact, rtrim, trim). Original newline style is preserved.
  - A hunk must include at least one change (`- ` or `+ `). Context-only hunks are invalid.
- Files are not deleted by this tool; only modified or cleared.
- Rewrites are applied before diffs when both are present for the same path.

### Example 1 — Full rewrite of a file

```xml
<CodeOutput version="first_draft">
  <Rewrite path="cmd/examples/great_test_writer.go/demo/add.go"><![CDATA[
package demo

func Add(a, b int) int {
  return a + b + 1 // intentional change
}
]]></Rewrite>
</CodeOutput>
```

### Example 2 — Patch a file with ApplyDiff

```xml
<CodeOutput>
  <ApplyDiff path="cmd/examples/great_test_writer.go/demo/add.go"><![CDATA[
@@ Add function
package demo
func Add(a, b int) int {
- return a + b
+ return a + b + 1
}
]]></ApplyDiff>
</CodeOutput>
```

### Tool call example

```json
{
  "code_output": "<CodeOutput>\n  <Rewrite path=\"README.md\"><![CDATA[\n# Project\nUpdated\n]]></Rewrite>\n</CodeOutput>"
}
```

Notes:
- Paths must correspond to files loaded in the active code container. Relative paths are written as-is; absolute paths are preserved.
- Only changed files are persisted.


