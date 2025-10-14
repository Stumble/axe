package code

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	cont "github.com/stumble/axe/code/container"
)

type ApplyEditToolSuite struct {
	suite.Suite
}

func TestApplyEditToolSuite(t *testing.T) { suite.Run(t, new(ApplyEditToolSuite)) }

// helper to invoke tool with given patch text embedded in CodeOutput XML
func (s *ApplyEditToolSuite) runToolWithPatch(cc *cont.CodeContainer, patch string) (string, error) {
	xml := fmt.Sprintf(`<CodeOutput><![CDATA[
%s
]]></CodeOutput>`, patch)
	payload := struct {
		CodeOutput string `json:"code_output"`
	}{CodeOutput: xml}
	argsBytes, err := json.Marshal(payload)
	s.Require().NoError(err)
	tool := &ApplyEditTool{Code: cc}
	return tool.InvokableRun(context.TODO(), string(argsBytes))
}

func (s *ApplyEditToolSuite) Test_Examples_Add_RewriteFiles() {
	dir := s.T().TempDir()
	foo := filepath.Join(dir, "foo.txt")
	fooTest := filepath.Join(dir, "foo_test.txt")

	patch := fmt.Sprintf(`*** Begin Patch
*** Add File: %s
+foo
+bar
+haha
*** Add File: %s
+foo_test
+bar_test
+haha_test
*** End Patch`, foo, fooTest)

	cc := cont.NewCodeContainer(map[string]string{})

	result, err := s.runToolWithPatch(cc, patch)
	s.Require().NoError(err)
	s.Contains(result, `apply_edit successfully applied edits:`)

	// Ensure files are written with expected content
	data1, err := os.ReadFile(foo)
	s.Require().NoError(err)
	s.Equal(`foo
bar
haha`, string(data1))

	data2, err := os.ReadFile(fooTest)
	s.Require().NoError(err)
	s.Equal(`foo_test
bar_test
haha_test`, string(data2))
}

func (s *ApplyEditToolSuite) Test_Examples_Update_ReplaceLine() {
	dir := s.T().TempDir()
	bar := filepath.Join(dir, "bar.txt")
	// seed existing file for update
	cc := cont.NewCodeContainer(map[string]string{
		bar: `context1
context2
context3
bar
context4
context5
context6
context7`,
	})

	patch := fmt.Sprintf(`*** Begin Patch
*** Update File: %s
 context1
 context2
 context3
-bar
+bar updated
 context4
 context5
 context6
*** End Patch`, bar)

	result, err := s.runToolWithPatch(cc, patch)
	s.Require().NoError(err)
	s.Contains(result, `apply_edit successfully applied edits:`)

	// Verify on disk
	data, err := os.ReadFile(bar)
	s.Require().NoError(err)
	s.Equal(`context1
context2
context3
bar updated
context4
context5
context6
context7`, string(data))
}

func (s *ApplyEditToolSuite) Test_Examples_MultipleFiles_UpdateAndAdd() {
	dir := s.T().TempDir()
	bar := filepath.Join(dir, "bar.txt")
	fooTest := filepath.Join(dir, "foo_test.txt")

	cc := cont.NewCodeContainer(map[string]string{
		bar: `context1
context2
context3
bar
context4
context5
context6`,
	})

	patch := fmt.Sprintf(`*** Begin Patch
*** Update File: %s
 context1
 context2
 context3
-bar
+bar updated
 context4
 context5
 context6
*** Add File: %s
+foo_test
+bar_test
*** End Patch`, bar, fooTest)

	result, err := s.runToolWithPatch(cc, patch)
	s.Require().NoError(err)
	s.Contains(result, `apply_edit successfully applied edits:`)

	// Verify updated file
	dataBar, err := os.ReadFile(bar)
	s.Require().NoError(err)
	s.Equal(`context1
context2
context3
bar updated
context4
context5
context6`, string(dataBar))

	// Verify added file
	dataFooTest, err := os.ReadFile(fooTest)
	s.Require().NoError(err)
	s.Equal(`foo_test
bar_test`, string(dataFooTest))
}
