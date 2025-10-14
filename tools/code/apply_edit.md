## apply_edit — Apply CodeOutput XML edits and persist changed files

Use this tool to apply code edits to the in-memory code container and write only the changed files back to disk.

- **Argument (JSON)**: `{"code_output": "<CodeOutput><![CDATA[...]]></CodeOutput>"}`
- **Edits Model**: Provide a `CodeOutput` XML with a V4A-inspired patch format text.

## Tool call example

```json
{
  "code_output": "<CodeOutput><![CDATA[...]]></CodeOutput>"
}
```

## V4A diff format

NOTE: When the change is huge, you should use Add action instead of Update action to just completely rewrite the file.

V4A is a custom format for diff/patch that makes it more convenient to add, remove, move, or edit code files. `apply_edit` effectively allows you to execute a diff/patch against a file, but the format of the diff specification is unique to this task, so pay careful attention to these instructions. To build a correct patch, you should call the apply_edit tool with a CodeOutput XML string.

```xml
<CodeOutput><![CDATA[***YOUR_PATCH***]]></CodeOutput>
```

Where ***YOUR_PATCH*** is the actual content of your patch, specified in the following V4A diff format.

*** [ACTION] File: [path/to/file] -> ACTION can be one of Add, Update, or Delete.
For each snippet of code that needs to be changed, repeat the following:
[context_before] -> See below for further instructions on context.
- [old_code] -> Precede the old code with a minus sign.
+ [new_code] -> Precede the new, replacement code with a plus sign.
[context_after] -> See below for further instructions on context.

For instructions on [context_before] and [context_after]:
- Context starts with a space character. For example, ` line1` is a context line for `line1` in `foo.txt`.
- By default, show 3 lines of code immediately above and 3 lines immediately below each change. If a change is within 3 lines of a previous change, do NOT duplicate the first change’s [context_after] lines in the second change’s [context_before] lines.
- If 3 lines of context is insufficient to uniquely identify the snippet of code within the file, use the @@ operator to indicate the class or function to which the snippet belongs. For instance, we might have:
@@ class BaseClass
[3 lines of pre-context]
- [old_code]
+ [new_code]
[3 lines of post-context]

- If a code block is repeated so many times in a class or function such that even a single @@ statement and 3 lines of context cannot uniquely identify the snippet of code, you can use multiple `@@` statements to jump to the right context. For instance:

@@ class BaseClass
@@ 	def method():
[3 lines of pre-context]
- [old_code]
+ [new_code]
[3 lines of post-context]

Note, then, that we do not use line numbers in this diff format, as the context is enough to uniquely identify code. An example of a message that you might pass as "input" to this function, in order to apply a patch, is shown below.


## Example

To patch file `foo.txt` to add `line2 updated` after `line2`, you can pass the following XML to the `apply_edit` tool:

file `foo.txt` before patching:
```text
foo
bar
haha
```

```xml
<CodeOutput><![CDATA[
*** Begin Patch
*** Update File: foo.txt
@@ foo
 foo
-bar
+bar updated
 haha
*** End of File
*** End Patch
]]></CodeOutput>
```

file `foo.txt` after patching:
```text
foo
bar updated
haha
```

Note:
- Always add a space character ' ' before context lines.
