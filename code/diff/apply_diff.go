package diff

import (
	"errors"
	"fmt"
	"strings"
)

const (
	errMsgNoHunks     = "no hunks found in patch"
	errMsgEmptyHunk   = "empty hunk with no operations"
	errMsgOnlyContext = "hunk has only context lines without any changes (missing '- ' or '+ ' lines)"
	errMsgMatchFailed = "unable to match context around hunk"
	errMsgNoChanges   = "WARNING: No changes detected - patch contains no '- ' or '+ ' lines"

	suggestionVerifyIndent = "Verify context lines match exactly (including spaces/tabs)"
	suggestionCodeChanged  = "Ensure the code hasn't changed since generating patch"
	suggestionAddMarkers   = "Add diff markers: '- ' for deletions, '+ ' for additions"
	suggestionCheckFormat  = "Check patch format: each change needs '- ' or '+ ' prefix"
)

// TODO:
// Anchor support is still problematic. Will revisit this later.

type hunk struct {
	anchors []string // optional '@@ ...' lines (used as substring hints)
	// ops encodes the sequence of context, deletion, and addition lines
	// within a single hunk. Unprefixed lines are context lines.
	ops []lineOp
}

type opKind int

const (
	opCtx opKind = iota
	opDel
	opAdd
)

type lineOp struct {
	kind opKind
	text string
}

func parsePatchText(patchText string) ([]hunk, error) {
	// Normalize to LF for parsing
	patchText = strings.ReplaceAll(patchText, "\r\n", "\n")
	lines := strings.Split(patchText, "\n")

	var hunks []hunk
	i := 0
	for i < len(lines) {
		// Skip leading blank lines between hunks
		for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
			i++
		}
		if i >= len(lines) {
			break
		}
		// Collect one hunk until a line that's exactly '---' or end.
		start := i
		for i < len(lines) && strings.TrimSpace(lines[i]) != "---" {
			i++
		}
		hunkLines := lines[start:i]

		// Move past the '---' separator if present
		if i < len(lines) && strings.TrimSpace(lines[i]) == "---" {
			i++
		}

		h, err := parseOneHunk(hunkLines)
		if err != nil {
			return nil, err
		}
		// Skip empty hunks (if someone put only blanks)
		if len(h.anchors) == 0 && len(h.ops) == 0 {
			continue
		}
		hunks = append(hunks, h)
	}

	if len(hunks) == 0 {
		return nil, errors.New(errMsgNoHunks)
	}
	return hunks, nil
}

func parseOneHunk(lines []string) (hunk, error) {
	var h hunk
	stage := "anchors" // anchors -> ops
	for idx := 0; idx < len(lines); idx++ {
		line := lines[idx]

		if stage == "anchors" {
			if strings.HasPrefix(line, "@@") {
				anchor := strings.TrimPrefix(line, "@@")
				anchor = strings.TrimLeft(anchor, " ")
				h.anchors = append(h.anchors, anchor)
				continue
			}
			stage = "ops"
			// fallthrough to process this line as an op
		}

		// ops stage: any order of context, deletions, and additions is allowed
		if strings.HasPrefix(line, "- ") {
			h.ops = append(h.ops, lineOp{kind: opDel, text: line[2:]})
			continue
		}
		if strings.HasPrefix(line, "+ ") {
			h.ops = append(h.ops, lineOp{kind: opAdd, text: line[2:]})
			continue
		}
		h.ops = append(h.ops, lineOp{kind: opCtx, text: line})
	}

	// Trim leading/trailing blank context lines for tidiness. Only trim contiguous
	// blanks at the start/end that are context lines.
	h.ops = trimBlankCtxEdges(h.ops)

	// Validate hunk format
	if err := validateHunk(h); err != nil {
		return h, err
	}

	return h, nil
}

// ApplyPatch applies a patch to a code block.
func ApplyPatch(original, patch string) (string, error) {
	// Detect and preserve original newline style for output
	outNL := "\n"
	if strings.Contains(original, "\r\n") {
		outNL = "\r\n"
	}

	origLF := strings.ReplaceAll(original, "\r\n", "\n")
	lines := splitPreserveEOF(origLF)

	hunks, err := parsePatchText(patch)
	if err != nil {
		return "", err
	}

	searchStart := 0
	for hi, h := range hunks {
		var pos int
		lines, pos, err = applyOneHunk(lines, h, searchStart)
		if err != nil {
			return "", fmt.Errorf("failed to apply hunk %d: %w", hi+1, err)
		}
		// Continue searching from where we just applied (stable forward progress).
		if pos >= 0 {
			searchStart = pos
		}
	}

	// Rejoin with the original newline style
	return strings.Join(lines, outNL), nil
}

// applyOneHunk finds the location for a hunk and applies it.
// It returns updated lines and the starting index where the hunk was applied.
func applyOneHunk(lines []string, h hunk, searchStart int) ([]string, int, error) {
	// If there are anchors, use them as substring hints to move searchStart forward.
	if len(h.anchors) > 0 {
		for _, a := range h.anchors {
			if a = strings.TrimSpace(a); a != "" {
				idx := findLineContaining(lines, a, searchStart)
				if idx != -1 {
					searchStart = idx
				}
			}
		}
	}

	// If there is neither context nor deletions, we cannot locate a position.
	// As a last resort, append additions to the end (preserve current behavior).
	if !hasCtxOrDel(h.ops) {
		add := collectAdds(h.ops)
		return append(lines, add...), len(lines), nil
	}

	// Three passes: exact -> rtrim -> trim
	modes := []string{"exact", "rtrim", "trim"}
	var nearMiss string
	for mode := 0; mode < 3; mode++ {
		for start := searchStart; start <= len(lines); start++ {
			if ok := matchOpsAt(lines, h.ops, start, mode); ok {
				newLines := applyOpsAt(lines, h.ops, start)
				return newLines, start, nil
			}
			if start == searchStart && nearMiss == "" {
				if partialMatch, lineNum := checkPartialMatch(lines, h.ops, start); partialMatch > 0 {
					nearMiss = fmt.Sprintf("Partial match (%d/%d lines) at line %d in %s mode",
						partialMatch, countCtxAndDel(h.ops), start+lineNum+1, modes[mode])
				}
			}
		}
	}

	// Build a friendly error with more details
	var preview []string
	ctxCount, delCount, addCount := 0, 0, 0
	for _, op := range h.ops {
		switch op.kind {
		case opDel:
			preview = append(preview, "- "+op.text)
			delCount++
		case opAdd:
			preview = append(preview, "+ "+op.text)
			addCount++
		default:
			preview = append(preview, op.text)
			ctxCount++
		}
	}

	msg := errMsgMatchFailed
	msg += fmt.Sprintf(" (context:%d, delete:%d, add:%d)", ctxCount, delCount, addCount)

	// If no changes detected, this is likely a format error
	if delCount == 0 && addCount == 0 {
		msg += "\n" + errMsgNoChanges
	}

	// Add near miss information if available
	if nearMiss != "" {
		msg += fmt.Sprintf("\nPartial match found: %s", nearMiss)
	}

	if len(h.anchors) > 0 {
		msg += fmt.Sprintf("\nAnchors: %q", h.anchors)
	}

	// Add specific suggestions based on the failure reason
	msg += "\nPossible causes:"
	msg += "\n  - " + suggestionVerifyIndent
	msg += "\n  - " + suggestionCodeChanged
	if delCount == 0 && addCount == 0 {
		msg += "\n  - " + suggestionCheckFormat
	}

	return nil, -1, fmt.Errorf("%s\nHunk preview:\n%s", msg, joinPreview(preview))
}

func splitPreserveEOF(s string) []string {
	// strings.Split preserves a trailing empty element when s ends with '\n'
	// which naturally represents a final blank line. That’s what we want.
	return strings.Split(s, "\n")
}

func joinPreview(xs []string) string {
	const maxLines = 12
	if len(xs) > maxLines {
		xs = append(xs[:maxLines-1], "…")
	}
	return strings.Join(xs, "\n")
}

// Matching modes:
// 0: exact
// 1: equal after rtrim (ignore trailing spaces/tabs/CR)
// 2: equal after full trim (ignore leading/trailing spaces)
func linesEqual(a, b string, mode int) bool {
	switch mode {
	case 0:
		return a == b
	case 1:
		return strings.TrimRight(a, " \t\r") == strings.TrimRight(b, " \t\r")
	default:
		return strings.TrimSpace(a) == strings.TrimSpace(b)
	}
}

func seqEqual(hay, needle []string, start int, mode int) bool {
	if start < 0 || start+len(needle) > len(hay) {
		return false
	}
	for i := 0; i < len(needle); i++ {
		if !linesEqual(hay[start+i], needle[i], mode) {
			return false
		}
	}
	return true
}

func findSequence(hay, needle []string, start, mode int) int {
	if len(needle) == 0 {
		return start
	}
	for i := start; i <= len(hay)-len(needle); i++ {
		if seqEqual(hay, needle, i, mode) {
			return i
		}
	}
	return -1
}

func findLineContaining(lines []string, needle string, start int) int {
	for i := start; i < len(lines); i++ {
		if strings.Contains(lines[i], needle) {
			return i
		}
	}
	return -1
}

func matchPreDelPost(lines, pre, del, post []string, start, mode int) int {
	// We look for [pre][del][post] starting from `start`.
	minLen := len(pre) + len(del) + len(post)
	for i := start; i <= len(lines)-minLen; i++ {
		if len(pre) > 0 && !seqEqual(lines, pre, i, mode) {
			continue
		}
		j := i + len(pre)
		if len(del) > 0 && !seqEqual(lines, del, j, mode) {
			continue
		}
		k := j + len(del)
		if len(post) > 0 && !seqEqual(lines, post, k, mode) {
			continue
		}
		return i
	}
	return -1
}

// (legacy insertion helpers removed; op-based applier supersedes them)

// ---- new helpers for op-sequence matching/applying -------------------------

func hasCtxOrDel(ops []lineOp) bool {
	for _, op := range ops {
		if op.kind == opCtx || op.kind == opDel {
			return true
		}
	}
	return false
}

func collectAdds(ops []lineOp) []string {
	var add []string
	for _, op := range ops {
		if op.kind == opAdd {
			add = append(add, op.text)
		}
	}
	return add
}

// matchOpsAt verifies that, starting at index "start", all context and deletion
// ops match against the original lines under the given whitespace mode.
func matchOpsAt(lines []string, ops []lineOp, start, mode int) bool {
	pos := start
	for _, op := range ops {
		switch op.kind {
		case opCtx, opDel:
			if pos >= len(lines) {
				return false
			}
			if !linesEqual(lines[pos], op.text, mode) {
				return false
			}
			pos++
		case opAdd:
			// does not consume original
		}
	}
	return true
}

// applyOpsAt constructs new lines by applying the op sequence at position start.
// It preserves original text for context lines, replaces deletions with nothing,
// and inserts additions at their positions.
func applyOpsAt(lines []string, ops []lineOp, start int) []string {
	left := append([]string{}, lines[:start]...)
	pos := start
	var mid []string
	for _, op := range ops {
		switch op.kind {
		case opCtx:
			mid = append(mid, lines[pos])
			pos++
		case opDel:
			// skip the original line
			pos++
		case opAdd:
			mid = append(mid, op.text)
		}
	}
	right := lines[pos:]
	out := make([]string, 0, len(left)+len(mid)+len(right))
	out = append(out, left...)
	out = append(out, mid...)
	out = append(out, right...)
	return out
}

// trimBlankCtxEdges removes blank context lines from the start and end of ops.
func trimBlankCtxEdges(ops []lineOp) []lineOp {
	i, j := 0, len(ops)
	for i < j {
		if ops[i].kind != opCtx || strings.TrimSpace(ops[i].text) != "" {
			break
		}
		i++
	}
	for j > i {
		if ops[j-1].kind != opCtx || strings.TrimSpace(ops[j-1].text) != "" {
			break
		}
		j--
	}
	return ops[i:j]
}

// validateHunk checks if the hunk has valid format
func validateHunk(h hunk) error {
	hasDelete := false
	hasAdd := false
	hasContext := false

	for _, op := range h.ops {
		switch op.kind {
		case opDel:
			hasDelete = true
		case opAdd:
			hasAdd = true
		case opCtx:
			hasContext = true
		}
	}

	// A hunk must have at least one operation
	if !hasContext && !hasDelete && !hasAdd {
		return errors.New(errMsgEmptyHunk)
	}

	if hasContext && !hasDelete && !hasAdd {
		return errors.New(errMsgOnlyContext)
	}

	return nil
}

// checkPartialMatch checks how many lines partially match
func checkPartialMatch(lines []string, ops []lineOp, start int) (int, int) {
	matched := 0
	pos := start
	for i, op := range ops {
		switch op.kind {
		case opCtx, opDel:
			if pos >= len(lines) {
				return matched, i
			}
			// Check if lines are somewhat similar (e.g., same first 10 chars)
			if len(lines[pos]) > 0 && len(op.text) > 0 {
				minLen := 10
				if len(lines[pos]) < minLen {
					minLen = len(lines[pos])
				}
				if len(op.text) < minLen {
					minLen = len(op.text)
				}
				if minLen > 0 && lines[pos][:minLen] == op.text[:minLen] {
					matched++
				}
			}
			pos++
		case opAdd:
			// doesn't consume lines
		}
	}
	return matched, 0
}

// countCtxAndDel counts context and delete operations
func countCtxAndDel(ops []lineOp) int {
	count := 0
	for _, op := range ops {
		if op.kind == opCtx || op.kind == opDel {
			count++
		}
	}
	return count
}
