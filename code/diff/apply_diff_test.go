package diff

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

// ---------- helpers ---------------------------------------------------------

type ApplyDiffSuite struct {
	suite.Suite
}

func TestApplyDiffSuite(t *testing.T) { suite.Run(t, new(ApplyDiffSuite)) }

func oneHunk(anchors []string, pre, del, add, post []string) string {
	var b []string
	for _, a := range anchors {
		// Match parser rule: any line starting with "@@" is an anchor
		if strings.TrimSpace(a) == "" {
			b = append(b, "@@")
		} else {
			b = append(b, "@@ "+a)
		}
	}
	b = append(b, pre...)
	for _, d := range del {
		b = append(b, "- "+d)
	}
	for _, a := range add {
		b = append(b, "+ "+a)
	}
	b = append(b, post...)
	return strings.Join(b, "\n")
}

func multiHunk(hunks ...string) string {
	return strings.Join(hunks, "\n---\n")
}

func (s *ApplyDiffSuite) mustApply(original, patch string) string {
	got, err := ApplyPatch(original, patch)
	s.Require().NoError(err)
	return got
}

// ---------- black-box tests for applyPatch ----------------------------------

func (s *ApplyDiffSuite) TestApplyPatch_ReplaceSimple() {
	orig := `export function sum(a, b) {
  return a + b;
}
`
	patch := oneHunk(
		[]string{"function sum(a, b)"},
		[]string{"export function sum(a, b) {"},
		[]string{"  return a + b;"},
		[]string{
			"  if (typeof a !== \"number\" || typeof b !== \"number\") {",
			"    throw new TypeError(\"sum expects numbers\");",
			"  }",
			"  return a + b;",
		},
		[]string{"}"},
	)
	got := s.mustApply(orig, patch)
	want := `export function sum(a, b) {
  if (typeof a !== "number" || typeof b !== "number") {
    throw new TypeError("sum expects numbers");
  }
  return a + b;
}
`
	s.Equal(want, got)
}

func (s *ApplyDiffSuite) TestApplyPatch_AddBetweenPrePost() {
	orig := `if (ok) {
}
`
	patch := oneHunk(
		nil,
		[]string{"if (ok) {"},
		nil,
		[]string{"  doSomething();"},
		[]string{"}"},
	)
	got := s.mustApply(orig, patch)
	want := `if (ok) {
  doSomething();
}
`
	s.Equal(want, got)
}

func (s *ApplyDiffSuite) TestApplyPatch_AddAfterPreOnly() {
	orig := `"use strict";
console.log("start");
`
	patch := oneHunk(
		nil,
		[]string{"\"use strict\";"},
		nil,
		[]string{"import x from \"y\";"},
		nil,
	)
	got := s.mustApply(orig, patch)
	want := `"use strict";
import x from "y";
console.log("start");
`
	s.Equal(want, got)
}

func (s *ApplyDiffSuite) TestApplyPatch_AddBeforePostOnly() {
	orig := `console.log("start");
module.exports = {};
`
	patch := oneHunk(
		nil,
		nil,
		nil,
		[]string{"const z = 1;"},
		[]string{"module.exports = {};"},
	)
	got := s.mustApply(orig, patch)
	want := `console.log("start");
const z = 1;
module.exports = {};
`
	s.Equal(want, got)
}

func (s *ApplyDiffSuite) TestApplyPatch_AddNoContext_Appends_NoFinalNewline() {
	orig := `a
b` // no trailing newline
	patch := oneHunk(nil, nil, nil, []string{"c"}, nil)
	got := s.mustApply(orig, patch)
	want := `a
b
c`
	s.Equal(want, got)
}

func (s *ApplyDiffSuite) TestApplyPatch_AddNoContext_Appends_WithFinalNewline_CurrentBehavior() {
	// Note: current implementation appends after the trailing empty slice element,
	// resulting in an extra blank line before the appended content.
	orig := `a
b
`
	patch := oneHunk(nil, nil, nil, []string{"c"}, nil)
	got := s.mustApply(orig, patch)
	want := `a
b

c`
	s.Equal(want, got)
}

func (s *ApplyDiffSuite) TestApplyPatch_DeleteOnly() {
	orig := `a
b
c
`
	patch := oneHunk(
		nil,
		[]string{"a"},
		[]string{"b"},
		nil,
		[]string{"c"},
	)
	got := s.mustApply(orig, patch)
	want := `a
c
`
	s.Equal(want, got)
}

func (s *ApplyDiffSuite) TestApplyPatch_MultipleHunks() {
	orig := `let a = 1;
let b = 2;
let c = 3;
`
	// 1) Replace a=1 with a=10 (using post context)
	h1 := oneHunk(
		nil,
		nil,
		[]string{"let a = 1;"},
		[]string{"let a = 10;"},
		[]string{"let b = 2;"},
	)
	// 2) Replace c=3 with c=30 (using pre context)
	h2 := oneHunk(
		nil,
		[]string{"let b = 2;"},
		[]string{"let c = 3;"},
		[]string{"let c = 30;"},
		nil,
	)
	patch := multiHunk(h1, h2)
	s.Require().Equal(`- let a = 1;
+ let a = 10;
let b = 2;
---
let b = 2;
- let c = 3;
+ let c = 30;`, multiHunk(h1, h2))
	got := s.mustApply(orig, patch)
	want := `let a = 10;
let b = 2;
let c = 30;
`
	s.Equal(want, got)
}

func (s *ApplyDiffSuite) TestApplyPatch_AnchorsDisambiguateSecondOccurrence() {
	orig := `function f() {
  // FIRST
  console.log("x");
}

function f() {
  // SECOND
  console.log("x");
}
`
	patch := oneHunk(
		[]string{""},
		[]string{"function f() {", "  // SECOND"},
		[]string{"  console.log(\"x\");"},
		[]string{"  console.log(\"y\");"},
		[]string{"}"},
	)
	got := s.mustApply(orig, patch)
	want := `function f() {
  // FIRST
  console.log("x");
}

function f() {
  // SECOND
  console.log("y");
}
`
	s.Equal(want, got)
}

func (s *ApplyDiffSuite) TestApplyPatch_PreserveCRLF() {
	orig := "function x(){\r\n  return 1;\r\n}\r\n"
	patch := oneHunk(
		nil,
		[]string{"function x(){"},
		[]string{"  return 1;"},
		[]string{"  return 2;"},
		[]string{"}"},
	)
	got := s.mustApply(orig, patch)
	// All newlines should be CRLF.
	want := "function x(){\r\n  return 2;\r\n}\r\n"
	s.Equal(want, got)
}

func (s *ApplyDiffSuite) TestApplyPatch_WhitespaceTolerance_Rtrim() {
	// Deletion line has trailing spaces in original; patch omits them.
	orig := `function x(){
  return a + b;    
}
`
	patch := oneHunk(
		nil,
		[]string{"function x(){"},
		[]string{"  return a + b;"},
		[]string{"  return a - b;"},
		[]string{"}"},
	)
	got := s.mustApply(orig, patch)
	want := `function x(){
  return a - b;
}
`
	s.Equal(want, got)
}

func (s *ApplyDiffSuite) TestApplyPatch_WhitespaceTolerance_Trim() {
	// Deletion line has extra leading spaces in original; patch uses none.
	orig := `function x(){
        return a + b;
}
`
	patch := oneHunk(
		nil,
		[]string{"function x(){"},
		[]string{"return a + b;"},
		[]string{"return a - b;"},
		[]string{"}"},
	)
	got := s.mustApply(orig, patch)
	want := `function x(){
return a - b;
}
`
	s.Equal(want, got)
}

func (s *ApplyDiffSuite) TestApplyPatch_Error_InvalidHunkOrder() {
	// "+ ..." followed by post, then "- ..." in post-phase => parse error
	patch := strings.Join([]string{
		"preline",
		"+ addline",
		"postline",
		"- oops-after-post",
	}, "\n")
	_, err := ApplyPatch("preline\npostline\n", patch)
	s.Require().Error(err)
	// With flexible op-order parsing, this is no longer a parse error.
	// We now fail at application time due to unmatched context.
	s.Contains(err.Error(), "unable to match context")
}

func (s *ApplyDiffSuite) TestApplyPatch_Error_NoHunks() {
	_, err := ApplyPatch("anything\n", "   \n   ")
	s.Require().Error(err)
	s.Contains(strings.ToLower(err.Error()), "no hunks")
}

func (s *ApplyDiffSuite) TestApplyPatch_Error_ContextNotFound() {
	orig := `a
b
`
	patch := oneHunk(
		nil,
		[]string{"X-not-present"},
		[]string{"b"},
		nil,
		nil,
	)
	_, err := ApplyPatch(orig, patch)
	s.Require().Error(err)
	s.Contains(err.Error(), "unable to match context")
}

func (s *ApplyDiffSuite) TestApplyPatch_AddBetweenPrePost_NotAdjacent_ShouldFail() {
	orig := `if (ok) {
  // comment
}
`
	patch := oneHunk(
		nil,
		[]string{"if (ok) {"},
		nil,
		[]string{"  inserted();"},
		[]string{"}"},
	)
	_, err := ApplyPatch(orig, patch)
	s.Require().Error(err)
	s.Contains(err.Error(), "unable to match context")
}

func (s *ApplyDiffSuite) TestApplyPatch_SequentialHunks_ProgressForward() {
	orig := `console.log("x");
console.log("x");
`
	// Replace first occurrence
	h1 := oneHunk(nil, nil,
		[]string{"console.log(\"x\");"},
		[]string{"console.log(\"y\");"},
		[]string{"console.log(\"x\");"},
	)
	// Then replace the (now) next occurrence
	h2 := oneHunk(nil, nil,
		[]string{"console.log(\"x\");"},
		[]string{"console.log(\"z\");"},
		nil,
	)
	patch := multiHunk(h1, h2)
	got := s.mustApply(orig, patch)
	want := `console.log("y");
console.log("z");
`
	s.Equal(want, got)
}

// ---------- light white-box checks of internal helpers ----------------------

func (s *ApplyDiffSuite) Test_linesEqual_Modes() {
	cases := []struct {
		a, b string
		mode int
		eq   bool
	}{
		{"x", "x", 0, true},
		{"x ", "x", 0, false},
		{"x ", "x", 1, true},  // rtrim ignores trailing spaces
		{" x", "x", 1, false}, // rtrim does not ignore leading
		{" x ", "x", 2, true}, // trim ignores both ends
	}
	for i, c := range cases {
		_ = i
		s.Equal(c.eq, linesEqual(c.a, c.b, c.mode))
	}
}

func (s *ApplyDiffSuite) Test_findSequence_and_matchPreDelPost() {
	lines := []string{"A", "B", "C", "D"}
	pos := findSequence(lines, []string{"B", "C"}, 0, 0)
	s.Equal(1, pos)
	// Match [pre][del][post] where del is empty (adjacency of pre and post)
	pos = matchPreDelPost(lines, []string{"B"}, nil, []string{"C"}, 0, 0)
	s.Equal(1, pos)
}

func (s *ApplyDiffSuite) Test_applyDiffBigFile() {
	orig := `
	export function sum(a, b) {
    return a + b;
	}
	export function minus(a, b) {
    return a + b;
	}
	`
	patchStr := `
  export function minus(a, b) {
-     return a + b;
+     return a - b;
}`

	hunk := oneHunk(
		nil,
		[]string{"", "  export function minus(a, b) {"},
		[]string{"    return a + b;"},
		[]string{"    return a - b;"},
		[]string{"}"},
	)
	s.Require().Equal(patchStr, hunk)

	got, err := ApplyPatch(orig, patchStr)
	s.Require().NoError(err)
	want := `
	export function sum(a, b) {
    return a + b;
	}
	export function minus(a, b) {
    return a - b;
	}
	`
	s.Equal(want, got)
}

func (s *ApplyDiffSuite) TestApplyPatch_Multiline_OneHunk() {
	orig := `
// 1
// 2
// 3
console.log("hello");
// 4
// 5
foo();
`

	patch := `
// 1
// 2
// 3
- console.log("hello");
+ console.log("world");
// 4
// 5
- foo();
+ bar();
`

	want := `
// 1
// 2
// 3
console.log("world");
// 4
// 5
bar();
`
	v, err := ApplyPatch(orig, patch)
	s.Require().NoError(err)
	s.Equal(want, v)
}
