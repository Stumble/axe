## apply_edit — Apply CodeOutput XML edits and persist changed files

Use this tool to apply code edits to the in-memory code container and write only the changed files back to disk.

- **Argument (JSON)**: `{"code_output": "<CodeOutput>...</CodeOutput>"}`
- **Edits Model**: Provide a `CodeOutput` XML with any mix of `Rewrite` (full-file replace) and `ApplyDiff` (patch) entries.

### Code Edit Specification

<code-edit-specification>

#### Rewrite

- Use <Rewrite path="path-of-the-file"><![CDATA[...]]></Rewrite> to create or overwrite a full code file.
- Rewrite is preferred over ApplyDiff unless the file is large and you want to make small, focused fixes.
- You can use Rewrite to create a new file.

#### ApplyDiff format (simple V4A-inspired)

- To make small, focused fixes, prefer a patch:
  <ApplyDiff path="path-of-the-file"><![CDATA[
  [YOUR_PATCH_HUNK_FOO]
  ---
  [YOUR_PATCH_HUNK_BAR]
  ---
  [... more hunks ...]
  ]]></ApplyDiff>

- You can issue multiple ApplyDiff edits to the same file. They will be applied in order. This allows you to make multiple small, focused fixes to the same file.
- Wrap nothing else; put only `[YOUR_PATCH]` between ApplyDiff tags.
- DO NOT USE @@ to add anchors to the patch. This is not supported.
- A patch consists of one or more **hunks**. Separate hunks with a line that contains only `---` (three dashes).
- In each hunk:
  - 3 lines (unless beginning of the file) of **pre-context** (unprefixed; must match the file exactly).
  - One or more lines to delete, each starting with `- ` (minus + space).
  - Zero or more lines to add, each starting with `+ ` (plus + space).
  - 3 lines (unless end of the file) of **post-context** (unprefixed; must match the file).

**Notes**
- Context helps the patch find the right spot; keep it short but unique.
- If your change only **adds** lines between two context blocks, omit the `- ` section.
- If your change only **removes** lines, omit the `+ ` section.
- Keep patches minimal and targeted—only include the lines you actually change plus the surrounding context.

#### Example
Initial code:
```javascript
export function sum(a, b) {
  return a + b;
}
```

Bug report: “sum() should guard against non-numbers.”

Fix with a patch:
<ApplyDiff path="main.js"><![CDATA[
export function sum(a, b) {
-   return a + b;
+   if (typeof a !== "number" || typeof b !== "number") {
+     throw new TypeError("sum expects numbers");
+   }
+   return a + b;
}
]]></ApplyDiff>

#### ApplyDiff Hard RULES CHECKLIST (**Important**)

1. A ApplyDiff **must** contain at least one added (`+ `) or removed (`- `) line.
2. **Context required.** Each hunk must include **3 lines of unprefixed context** before and after the change, except at file boundaries.
3. **No raw, unprefixed source blocks.** Any non-empty line that is not:
   * a context line (exactly matching the target file around the change), or
   * a diff line starting with `+ ` or `- `, or
   * a hunk separator line `---`
     ⇒ **is invalid** inside `<ApplyDiff>`.
4. **Single-target edit.** `path` must match an existing file.
5. **Patch scope.** The edit must touch **one file only** (the file named by the target `path`).

## Fast validator checklist (pre-execution)

* [ ] `path` matches an existing file
* [ ] Contains at least one `+ ` or `- ` line
* [ ] Every change has 3 lines of unprefixed context (unless at start/end of file)
* [ ] No large unprefixed blocks (i.e., not a full program dump)
* [ ] No @@ anchors

### ✅ Correct minimal patch

```xml
<ApplyDiff path="main.js"><![CDATA[
  const validation = {
-    official_data_available: official.total_supply !== null,
+    official_present: typeof official.total_supply === 'number',
    aggregator_data_count: validation.aggregator_comparison
      .filter(a => a.reported_supply !== null).length,
    data_consistency: validation.overall_validation.includes('Strong')
  }
]]></ApplyDiff>
```

### ❌ Incorrect (full body pasted; no `+`/`-`)

```xml
<ApplyDiff path="main.js"><![CDATA[
function buildDataBrief(...) {
  // entire rewritten file dumped here
}
]]></ApplyDiff>
```

### ❌ Incorrect (missing context)

```xml
<ApplyDiff path="main.js"><![CDATA[
- const x = 1;
+ const x = 2;
]]></ApplyDiff>
```

*Fix:* include 3 context lines above and below, unless at top/bottom of file.

</code-edit-specification>

### CodeOutput XML schema

```xml
<CodeOutput version="optional-version">
  <Rewrite path="/abs/or/relative/path.ext"><![CDATA[
<full new file content>
]]></Rewrite>

  <ApplyDiff path="/abs/or/relative/path.ext"><![CDATA[
  [YOUR_PATCH_HUNK_FOO]
  ---
  [YOUR_PATCH_HUNK_BAR]
  ---
  [... more hunks ...]
]]></ApplyDiff>
</CodeOutput>
```

- **`Rewrite`**: Replaces the entire file content. To clear a file without deleting it, provide an empty CDATA.
- **`ApplyDiff`**: Applies a patch to the current file content.
- Rewrites are applied before diffs when both are present for the same path.

### Tool call example

```json
{
  "code_output": "<CodeOutput>\n  <Rewrite path=\"README.md\"><![CDATA[\n# Project\nUpdated\n]]></Rewrite>\n</CodeOutput>"
}
```

Notes:
- Paths must correspond to files loaded in the active code container. Relative paths are written as-is; absolute paths are preserved.
- Only changed files are persisted.
