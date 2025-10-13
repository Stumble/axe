## apply_edit — Apply CodeOutput XML edits and persist changed files

Use this tool to apply code edits to the in-memory code container and write only the changed files back to disk.

- **Argument (JSON)**: `{"code_output": "<CodeOutput><![CDATA[...]]></CodeOutput>"}`
- **Edits Model**: Provide a `CodeOutput` XML with a V4A-inspired patch format text.

### Tool call example

```json
{
  "code_output": "<CodeOutput><![CDATA[...]]></CodeOutput>"
}
```
