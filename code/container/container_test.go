package container

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type ContextSuite struct{ suite.Suite }

func TestContextSuite(t *testing.T) { suite.Run(t, new(ContextSuite)) }

// helper to construct a single-hunk patch similar to code/diff tests
func mkPatch(pre, del, add, post []string) string {
	var b []string
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

func (s *ContextSuite) TestBuildCodeInput_SortingAndFilter() {
	files := map[string]string{"b.txt": "B", "a.txt": "A", "c.txt": "C"}

	ci := BuildCodeInput(files, nil)
	s.Require().Len(ci.Files, 3)
	s.Equal("a.txt", ci.Files[0].Path)
	s.Equal("b.txt", ci.Files[1].Path)
	s.Equal("c.txt", ci.Files[2].Path)

	ci2 := BuildCodeInput(files, []string{"c.txt", "a.txt", "a.txt", "d.txt"})
	s.Require().Len(ci2.Files, 2)
	s.Equal([]string{"a.txt", "c.txt"}, []string{ci2.Files[0].Path, ci2.Files[1].Path})
}

func (s *ContextSuite) TestRenderCodeInput_CDATA_And_PathAttr() {
	files := map[string]string{"a.go": "package a\nfunc A(){}\n", "b.txt": "hello"}
	xml, err := BuildCodeInput(files, []string{"b.txt", "a.go"}).ToXML()
	s.Require().NoError(err)
	s.Contains(xml, "<CodeInput>")
	// paths present
	s.Contains(xml, "<File path=\"a.go\">")
	s.Contains(xml, "<File path=\"b.txt\">")
	// CDATA present
	s.Contains(xml, "<![CDATA[")
	s.Contains(xml, "]]>")
	// content preserved
	s.Contains(xml, "package a\nfunc A(){}")
}

func (s *ContextSuite) TestRenderCodeInput_CDATA_Splits_EndMarker() {
	files := map[string]string{"x": "begin]]>end"}
	xml, err := BuildCodeInput(files, nil).ToXML()
	s.Require().NoError(err)
	s.Contains(xml, "]]]]><![CDATA[>")
}

func (s *ContextSuite) TestParseCodeOutput_Unmarshal() {
	outXML := `
<CodeOutput version="t1">
  <Rewrite path="a.txt"><![CDATA[
new content
]]></Rewrite>
  <ApplyDiff path="b.txt"><![CDATA[
line1
- line2
+ line2 changed
line3
]]></ApplyDiff>
</CodeOutput>`
	out, err := ParseCodeOutput(outXML)
	s.Require().NoError(err)
	s.Equal("t1", out.Version)
	s.Require().Len(out.Rewrites, 1)
	s.Require().Len(out.ApplyDiffs, 1)
	s.Equal("a.txt", out.Rewrites[0].Path)
	s.Equal("b.txt", out.ApplyDiffs[0].Path)
	s.Contains(out.Rewrites[0].Content, "new content")
	s.Contains(out.ApplyDiffs[0].Patch, "- line2")
}

func (s *ContextSuite) TestApplyEdits_Rewrite_And_Diff() {
	files := map[string]string{
		"a.txt": "old\n",
		"b.txt": "line1\nline2\nline3\n",
	}
	patch := mkPatch([]string{"line1"}, []string{"line2"}, []string{"line2 changed"}, []string{"line3"})
	out := CodeOutput{
		Version:    "v",
		Rewrites:   []CodeOutputRewrite{{Path: "a.txt", Content: "new\n"}},
		ApplyDiffs: []CodeOutputDiff{{Path: "b.txt", Patch: patch}},
	}
	updated, changed, err := ApplyEdits(files, out)
	s.Require().NoError(err)
	s.ElementsMatch([]string{"a.txt", "b.txt"}, changed)
	s.Equal("new\n", updated["a.txt"])
	s.Equal("line1\nline2 changed\nline3\n", updated["b.txt"])
}

func (s *ContextSuite) TestCodeContainer_Apply_And_Files() {
	cc := NewCodeContainer("", map[string]string{"f.js": "a\nb\nc\n", "x": "1"})
	patch := mkPatch([]string{"a"}, []string{"b"}, []string{"B"}, []string{"c"})
	out := CodeOutput{ApplyDiffs: []CodeOutputDiff{{Path: "f.js", Patch: patch}}, Rewrites: []CodeOutputRewrite{{Path: "x", Content: "2"}}}
	changed, err := cc.Apply(out)
	s.Require().NoError(err)
	s.ElementsMatch([]string{"f.js", "x"}, changed)
	files := cc.Files()
	s.Equal("2", files["x"])
	s.Equal("a\nB\nc\n", files["f.js"])
}

func (s *ContextSuite) TestCodeContainer_WriteToFiles() {
	dir := s.T().TempDir()
	cc := NewCodeContainer(dir, map[string]string{"d1/f.txt": "alpha", "f2.txt": "beta"})
	wrote, err := cc.WriteToFiles(nil)
	s.Require().NoError(err)
	s.ElementsMatch([]string{"d1/f.txt", "f2.txt"}, wrote)
	// verify on disk
	data1, err := os.ReadFile(filepath.Join(dir, "d1", "f.txt"))
	s.Require().NoError(err)
	s.Equal("alpha", string(data1))
	data2, err := os.ReadFile(filepath.Join(dir, "f2.txt"))
	s.Require().NoError(err)
	s.Equal("beta", string(data2))
}

func (s *ContextSuite) TestCodeOutput_WriteToFiles_Wrapper() {
	dir := s.T().TempDir()
	// seed file
	require.NoError(s.T(), os.MkdirAll(dir, 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "t.txt"), []byte("x\ny\n"), 0o644))

	cc := NewCodeContainer(dir, map[string]string{"t.txt": "x\ny\n"})
	patch := mkPatch([]string{"x"}, []string{"y"}, []string{"z"}, nil)
	out := CodeOutput{ApplyDiffs: []CodeOutputDiff{{Path: "t.txt", Patch: patch}}}
	changed, err := cc.Apply(out)
	s.Require().NoError(err)
	cc.WriteToFiles(changed)
	s.Require().NoError(err)
	s.Equal([]string{"t.txt"}, changed)
	// in-memory updated
	s.Equal("x\nz\n", cc.Files()["t.txt"])
	// on-disk updated
	data, err := os.ReadFile(filepath.Join(dir, "t.txt"))
	s.Require().NoError(err)
	s.Equal("x\nz\n", string(data))
}
