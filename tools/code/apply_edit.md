## apply_edit — The Code Editing Tool

Use this tool to edit code files.

- **Argument (JSON)**: `{"code_output": "<CodeOutput><![CDATA[...]]></CodeOutput>"}`
- **Edits Model**: Provide a `CodeOutput` XML with a V4A patch format text.

The most important principles are:

1. Use Add action instead of Update action to just completely rewrite the file. This is preferred. Unless your changes is very targeted and focused that only contains a few lines of code. (less than 20 lines of code).
2. Always wrap the v4a patch text in `<![CDATA[...]]>` tags within the <CodeOutput> XML tag.
3. You can edit multiple files in a single call.

## Tool call example

```json
{
  "code_output": "<CodeOutput><![CDATA[...]]></CodeOutput>"
}
```

## V4A diff format

NOTE: When the change is huge, you should use Add action instead of Update action to just completely rewrite the file.

V4A is a custom format for diff/patch/replace that makes it more convenient to add, remove, move, or edit code files. `apply_edit` effectively allows you to execute a diff/patch/replace against multiple files at once. The format of the diff specification is unique to this task, so pay careful attention to these instructions. To build a correct patch, you should call the apply_edit tool with a CodeOutput XML string.

```xml
<CodeOutput><![CDATA[***YOUR_PATCH***]]></CodeOutput>
```

Where ***YOUR_PATCH*** is the actual content of your patch, specified in the following V4A diff format:

*** [ACTION] File: [path/to/file] -> ACTION can be one of Add, Update, or Delete.
For each snippet of code that needs to be changed, repeat the following:
[context_before] -> See below for further instructions on context. NOTE: You must put a space character ' ' before the context lines.
- [old_code] -> Precede the old code with a minus sign.
+ [new_code] -> Precede the new, replacement code with a plus sign.
[context_after] -> See below for further instructions on context. NOTE: You must put a space character ' ' before the context lines.

For instructions on [context_before] and [context_after]:
- Context starts with a space character. For example, ` line1` is a context line for `line1` in `foo.txt`.
- By default, show 3 lines of code immediately above and 3 lines immediately below each change. If a change is within 3 lines of a previous change, do NOT duplicate the first change’s [context_after] lines in the second change’s [context_before] lines.
- You can use the `*** End of File` keyword to match the end of the file. This is useful when you want to edit the last few lines of the file, where you do not have enough context after the change. This is only available for Update action for context lines.
- (very rare case, use only if absolutely necessary) If 3 lines of context is insufficient to uniquely identify the snippet of code within the file, use the @@ operator to indicate the class or function to which the snippet belongs. For instance, we might have:
@@ class BaseClass
[3 lines of pre-context]
- [old_code]
+ [new_code]
[3 lines of post-context]

- (extremely rare case, use only if absolutely necessary) If a code block is repeated so many times in a class or function such that even a single @@ statement and 3 lines of context cannot uniquely identify the snippet of code, you can use multiple `@@` statements to jump to the right context. For instance:

@@ class BaseClass
@@ 	def method():
[3 lines of pre-context]
- [old_code]
+ [new_code]
[3 lines of post-context]

Note, then, that we do not use line numbers in this diff format, as the context is enough to uniquely identify edited code.

## Example

### Add (Just Rewrite the File, The preferred way)

To add a new file or completely rewrite a file, you can pass the following XML to the `apply_edit` tool:

file `foo.txt` before patching:

```text
some random content
random words
```

The patch XML:
```xml
<CodeOutput><![CDATA[
*** Begin Patch
*** Add File: foo.txt
+foo
+bar
+haha
*** Add File: foo_test.txt
+foo_test
+bar_test
+haha_test
*** End Patch
]]></CodeOutput>
```

file `foo.txt` after patching:
```text
foo
bar
haha
```

file `foo_test.txt` after patching:
```text
foo_test
bar_test
haha_test
```

### Update

To patch file `bar.txt` replace a line of `bar` with `bar updated`, you can pass the following XML to the `apply_edit` tool:

file `bar.txt` before patching:
```text
context1
context2
context3
bar
context4
context5
context6
context7
```

```xml
<CodeOutput><![CDATA[
*** Begin Patch
*** Update File: bar.txt
 context1
 context2
 context3
-bar
+bar updated
 context4
 context5
 context6
*** End Patch
]]></CodeOutput>
```

file `bar.txt` after patching:
```text
context1
context2
context3
bar updated
context4
context5
context6
context7
```

Note:
- Always add a space character ' ' before context lines.

### Multiple files

To patch multiple files, you can pass the following XML to the `apply_edit` tool:

```xml
<CodeOutput><![CDATA[
*** Begin Patch
*** Update File: bar.txt
 context1
 context2
 context3
-bar
+bar updated
 context4
 context5
 context6
*** Add File: foo_test.txt
+foo_test
+bar_test
*** End Patch
]]></CodeOutput>
```
