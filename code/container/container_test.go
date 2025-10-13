package container

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"
)

type ContextSuite struct{ suite.Suite }

func TestContextSuite(t *testing.T) { suite.Run(t, new(ContextSuite)) }

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

func (s *ContextSuite) TestCodeContainer_WriteToFiles() {
	dir := s.T().TempDir()
	cc := NewCodeContainer(map[string]string{
		filepath.Join(dir, "d1", "f.txt"): "alpha",
		filepath.Join(dir, "f2.txt"):      "beta",
	})
	err := cc.WriteToFiles()
	s.Require().NoError(err)
	// compare relative to base dir for determinism
	rel := make([]string, 0, len(cc.Files()))
	for p := range cc.Files() {
		rp, rerr := filepath.Rel(dir, p)
		s.Require().NoError(rerr)
		rel = append(rel, rp)
	}
	s.ElementsMatch([]string{"d1/f.txt", "f2.txt"}, rel)
	// verify on disk
	data1, err := os.ReadFile(filepath.Join(dir, "d1", "f.txt"))
	s.Require().NoError(err)
	s.Equal("alpha", string(data1))
	data2, err := os.ReadFile(filepath.Join(dir, "f2.txt"))
	s.Require().NoError(err)
	s.Equal("beta", string(data2))
}

func (s *ContextSuite) TestCodeContainer_Apply() {
	dir := s.T().TempDir()
	updatePath := filepath.Join(dir, "update.txt")
	deletePath := filepath.Join(dir, "delete.txt")
	newPath := filepath.Join(dir, "new.txt")

	// Setup initial files
	cc := NewCodeContainer(map[string]string{
		updatePath: "line1\nline2\nline3",
		deletePath: "will be deleted",
	})

	// Test applying a patch with updates, deletions and additions
	patchText := `*** Begin Patch
*** Update File: ` + updatePath + `
@@ line1
 line1
-line2
+line2-modified
 line3
*** End of File
*** Delete File: ` + deletePath + `
*** Add File: ` + newPath + `
+new content
*** End Patch`

	output := CodeOutput{Patch: patchText}
	msg, err := cc.Apply(output)
	s.Require().NoError(err)
	s.Equal("Done!", msg)

	// Verify the updates in memory
	files := cc.Files()
	s.Equal("line1\nline2-modified\nline3", files[updatePath])
	s.Equal("new content", files[newPath])
	_, exists := files[deletePath]
	s.False(exists, "deleted file should not be in container")
}

func (s *ContextSuite) TestCodeContainer_Apply_InvalidPatch() {
	cc := NewCodeContainer(map[string]string{
		"test.txt": "content",
	})

	// Test with invalid patch format (missing sentinels)
	output := CodeOutput{Patch: "invalid patch text"}
	_, err := cc.Apply(output)
	s.Require().Error(err)
	s.Contains(err.Error(), "Patch text must start with")
}

func (s *ContextSuite) TestCodeContainer_WriteToFiles_WithDeletes() {
	dir := s.T().TempDir()

	// Create initial files on disk
	f1Path := filepath.Join(dir, "keep.txt")
	f2Path := filepath.Join(dir, "delete.txt")
	err := os.WriteFile(f1Path, []byte("keep this"), 0o644)
	s.Require().NoError(err)
	err = os.WriteFile(f2Path, []byte("delete this"), 0o644)
	s.Require().NoError(err)

	// Verify both files exist
	_, err = os.Stat(f1Path)
	s.Require().NoError(err)
	_, err = os.Stat(f2Path)
	s.Require().NoError(err)

	// Create container with only one file and mark the other as deleted
	cc := NewCodeContainer(map[string]string{
		f1Path: "keep this modified",
	})
	// Simulate deletion
	err = cc.Remove(f2Path)
	s.Require().NoError(err)

	// Write changes to disk
	err = cc.WriteToFiles()
	s.Require().NoError(err)

	// Verify kept file was updated
	data, err := os.ReadFile(f1Path)
	s.Require().NoError(err)
	s.Equal("keep this modified", string(data))

	// Verify deleted file is gone
	_, err = os.Stat(f2Path)
	s.True(os.IsNotExist(err), "deleted file should not exist on disk")
}

func (s *ContextSuite) TestCodeContainer_WriteToFiles_PreservesPermissions() {
	dir := s.T().TempDir()
	execPath := filepath.Join(dir, "script.sh")

	// Create an executable file
	err := os.WriteFile(execPath, []byte("#!/bin/bash\necho hello"), 0o755)
	s.Require().NoError(err)

	// Read it into container and modify
	cc := NewCodeContainer(map[string]string{
		execPath: "#!/bin/bash\necho world",
	})

	// Write back
	err = cc.WriteToFiles()
	s.Require().NoError(err)

	// Check permissions are preserved
	info, err := os.Stat(execPath)
	s.Require().NoError(err)
	s.Equal(os.FileMode(0o755), info.Mode().Perm())

	// Verify content was updated
	data, err := os.ReadFile(execPath)
	s.Require().NoError(err)
	s.Equal("#!/bin/bash\necho world", string(data))
}
