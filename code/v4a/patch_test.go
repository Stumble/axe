package v4a

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/suite"
)

func TestPatchSuite(t *testing.T) {
	suite.Run(t, new(PatchSuite))
}

type PatchSuite struct {
	suite.Suite
}

func (s *PatchSuite) stringPtr(v string) *string {
	return &v
}

func (s *PatchSuite) TestSplitLinesLikePython() {
	cases := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "no trailing newline",
			input: "alpha\nbeta",
			want:  []string{"alpha", "beta"},
		},
		{
			name:  "trailing newline dropped",
			input: "one\n",
			want:  []string{"one"},
		},
		{
			name:  "empty string",
			input: "",
			want:  nil,
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			got := splitLinesLikePython(tc.input)
			s.Require().Equal(tc.want, got)
		})
	}
}

func (s *PatchSuite) TestFindContext() {
	cases := []struct {
		name      string
		lines     []string
		context   []string
		start     int
		eof       bool
		wantIndex int
		wantFuzz  int
	}{
		{
			name:      "strict match",
			lines:     []string{"alpha", "beta", "gamma"},
			context:   []string{"beta"},
			start:     0,
			wantIndex: 1,
			wantFuzz:  0,
		},
		{
			name:      "rstrip match",
			lines:     []string{"foo  ", "bar"},
			context:   []string{"foo"},
			start:     0,
			wantIndex: 0,
			wantFuzz:  1,
		},
		{
			name:      "strip match",
			lines:     []string{"  foo  ", "bar"},
			context:   []string{"foo"},
			start:     0,
			wantIndex: 0,
			wantFuzz:  100,
		},
		{
			name:      "not found",
			lines:     []string{"one", "two"},
			context:   []string{"missing"},
			start:     0,
			wantIndex: -1,
			wantFuzz:  0,
		},
		{
			name:      "eof fallback",
			lines:     []string{"one", "two", "three"},
			context:   []string{"one"},
			start:     0,
			eof:       true,
			wantIndex: 0,
			wantFuzz:  10_000,
		},
		{
			name:      "eof direct hit",
			lines:     []string{"line1", "line2"},
			context:   []string{"line2"},
			start:     0,
			eof:       true,
			wantIndex: 1,
			wantFuzz:  0,
		},
	}

	for _, tc := range cases {
		tc := tc
		s.Run(tc.name, func() {
			gotIndex, gotFuzz := findContext(tc.lines, tc.context, tc.start, tc.eof)
			s.Equal(tc.wantIndex, gotIndex)
			s.Equal(tc.wantFuzz, gotFuzz)
		})
	}
}

func (s *PatchSuite) TestGetUpdatedFile() {
	cases := []struct {
		name    string
		text    string
		action  PatchAction
		want    string
		wantErr string
	}{
		{
			name: "single replacement",
			text: "line1\nline2\nline3",
			action: PatchAction{
				Type: ActionUpdate,
				Chunks: []Chunk{
					{
						OrigIndex: 1,
						DelLines:  []string{"line2"},
						InsLines:  []string{"line-x"},
					},
				},
			},
			want: "line1\nline-x\nline3",
		},
		{
			name: "insertion at start",
			text: "line1\nline2",
			action: PatchAction{
				Type: ActionUpdate,
				Chunks: []Chunk{
					{
						OrigIndex: 0,
						InsLines:  []string{"new"},
					},
				},
			},
			want: "new\nline1\nline2",
		},
		{
			name: "chunk past end",
			text: "only",
			action: PatchAction{
				Type: ActionUpdate,
				Chunks: []Chunk{
					{
						OrigIndex: 5,
					},
				},
			},
			wantErr: "exceeds file length",
		},
		{
			name: "overlapping chunks",
			text: "a\nb\nc",
			action: PatchAction{
				Type: ActionUpdate,
				Chunks: []Chunk{
					{
						OrigIndex: 1,
						DelLines:  []string{"b"},
					},
					{
						OrigIndex: 1,
						InsLines:  []string{"x"},
					},
				},
			},
			wantErr: "overlapping chunks",
		},
	}

	for _, tc := range cases {
		tc := tc
		s.Run(tc.name, func() {
			got, err := getUpdatedFile(tc.text, tc.action, "test.txt")
			if tc.wantErr != "" {
				s.Require().Error(err)
				s.Contains(err.Error(), tc.wantErr)
				return
			}
			s.Require().NoError(err)
			s.Equal(tc.want, got)
		})
	}
}

func (s *PatchSuite) TestPatchToCommit() {
	addContent := "new file contents"
	cases := []struct {
		name    string
		patch   Patch
		orig    map[string]string
		wantErr string
		check   func(Commit)
	}{
		{
			name: "happy path",
			patch: Patch{Actions: map[string]*PatchAction{
				"delete.txt": {
					Type: ActionDelete,
				},
				"add.txt": {
					Type:    ActionAdd,
					NewFile: s.stringPtr(addContent),
				},
				"update.txt": {
					Type:     ActionUpdate,
					MovePath: "renamed.txt",
					Chunks: []Chunk{
						{
							OrigIndex: 1,
							DelLines:  []string{"old"},
							InsLines:  []string{"new"},
						},
					},
				},
			}},
			orig: map[string]string{
				"delete.txt": "legacy",
				"update.txt": "keep\nold",
			},
			check: func(commit Commit) {
				del, ok := commit.Changes["delete.txt"]
				s.Require().True(ok)
				s.Equal(ActionDelete, del.Type)
				s.Require().NotNil(del.OldContent)
				s.Equal("legacy", *del.OldContent)

				add, ok := commit.Changes["add.txt"]
				s.Require().True(ok)
				s.Equal(ActionAdd, add.Type)
				s.Require().NotNil(add.NewContent)
				s.Equal(addContent, *add.NewContent)

				upd, ok := commit.Changes["update.txt"]
				s.Require().True(ok)
				s.Equal(ActionUpdate, upd.Type)
				s.Require().NotNil(upd.OldContent)
				s.Equal("keep\nold", *upd.OldContent)
				s.Require().NotNil(upd.NewContent)
				s.Equal("keep\nnew", *upd.NewContent)
				s.Equal("renamed.txt", upd.MovePath)
			},
		},
		{
			name: "missing add content",
			patch: Patch{Actions: map[string]*PatchAction{
				"add.txt": {
					Type: ActionAdd,
				},
			}},
			orig:    map[string]string{},
			wantErr: "ADD action without file content",
		},
		{
			name: "bad update chunk",
			patch: Patch{Actions: map[string]*PatchAction{
				"broken.txt": {
					Type:   ActionUpdate,
					Chunks: []Chunk{{OrigIndex: 5}},
				},
			}},
			orig:    map[string]string{"broken.txt": "text"},
			wantErr: "exceeds file length",
		},
	}

	for _, tc := range cases {
		tc := tc
		s.Run(tc.name, func() {
			commit, err := patchToCommit(tc.patch, tc.orig)
			if tc.wantErr != "" {
				s.Require().Error(err)
				s.Contains(err.Error(), tc.wantErr)
				return
			}
			s.Require().NoError(err)
			s.Require().NotNil(tc.check)
			tc.check(commit)
		})
	}
}

func (s *PatchSuite) TestIdentifyFilesHelpers() {
	patchText := "*** Begin Patch\n" +
		"*** Update File: foo.txt\n" +
		"@@ foo\n" +
		" foo\n" +
		"*** Delete File: bar.txt\n" +
		"*** Add File: baz.txt\n" +
		"+line\n" +
		"*** End Patch"

	s.Equal([]string{"foo.txt", "bar.txt"}, identifyFilesNeeded(patchText))
	s.Equal([]string{"baz.txt"}, identifyFilesAdded(patchText))
}

func (s *PatchSuite) TestApplyCommit() {
	newContent := "updated"
	cases := []struct {
		name        string
		commit      Commit
		wantErr     string
		wantWrites  map[string]string
		wantRemoves []string
	}{
		{
			name: "full lifecycle",
			commit: Commit{Changes: map[string]FileChange{
				"delete.txt": {
					Type: ActionDelete,
				},
				"add.txt": {
					Type:       ActionAdd,
					NewContent: s.stringPtr("created"),
				},
				"move.txt": {
					Type:       ActionUpdate,
					NewContent: s.stringPtr(newContent),
					MovePath:   "moved.txt",
				},
			}},
			wantWrites: map[string]string{
				"add.txt":   "created",
				"moved.txt": newContent,
			},
			wantRemoves: []string{"delete.txt", "move.txt"},
		},
		{
			name: "add missing content",
			commit: Commit{Changes: map[string]FileChange{
				"add.txt": {Type: ActionAdd},
			}},
			wantErr: "has no content",
		},
	}

	for _, tc := range cases {
		tc := tc
		s.Run(tc.name, func() {
			writes := make(map[string]string)
			var removes []string

			err := applyCommit(tc.commit,
				func(path, content string) error {
					writes[path] = content
					return nil
				},
				func(path string) error {
					removes = append(removes, path)
					return nil
				},
			)

			if tc.wantErr != "" {
				s.Require().Error(err)
				s.Contains(err.Error(), tc.wantErr)
				return
			}

			s.Require().NoError(err)
			s.Equal(tc.wantWrites, writes)
			s.ElementsMatch(tc.wantRemoves, removes)
		})
	}
}

type fakeFileSystem struct {
	files   map[string]string
	writes  map[string]string
	removes []string
}

func newFakeFileSystem(seed map[string]string) *fakeFileSystem {
	files := make(map[string]string, len(seed))
	for k, v := range seed {
		files[k] = v
	}
	return &fakeFileSystem{
		files:  files,
		writes: make(map[string]string),
	}
}

func (fs *fakeFileSystem) Open(path string) (string, error) {
	txt, ok := fs.files[path]
	if !ok {
		return "", fmt.Errorf("missing file: %s", path)
	}
	return txt, nil
}

func (fs *fakeFileSystem) Write(path, content string) error {
	fs.files[path] = content
	fs.writes[path] = content
	return nil
}

func (fs *fakeFileSystem) Remove(path string) error {
	delete(fs.files, path)
	fs.removes = append(fs.removes, path)
	return nil
}

func (s *PatchSuite) TestApplyPatch() {
	cases := []struct {
		name        string
		fs          *fakeFileSystem
		patchText   string
		wantFiles   map[string]string
		wantRemoves []string
	}{
		{
			name: "update delete add with move",
			fs: newFakeFileSystem(map[string]string{
				"foo.txt": "line1\nline2",
				"bar.txt": "old",
			}),
			patchText: "*** Begin Patch\n" +
				"*** Update File: foo.txt\n" +
				"*** Move to: foo-renamed.txt\n" +
				"@@ line1\n" +
				" line1\n" +
				"-line2\n" +
				"+line2 updated\n" +
				"*** End of File\n" +
				"*** Delete File: bar.txt\n" +
				"*** Add File: new.txt\n" +
				"+fresh\n" +
				"*** End Patch",
			wantFiles: map[string]string{
				"foo-renamed.txt": "line1\nline2 updated",
				"new.txt":         "fresh",
			},
			wantRemoves: []string{"foo.txt", "bar.txt"},
		},
	}

	for _, tc := range cases {
		tc := tc
		s.Run(tc.name, func() {
			result, err := ApplyPatch(tc.fs, tc.patchText)
			s.Require().NoError(err)
			s.Equal("Done!", result)
			for path, content := range tc.wantFiles {
				got, ok := tc.fs.files[path]
				s.Require().True(ok, "expected file %s to exist", path)
				s.Equal(content, got)
			}
			s.ElementsMatch(tc.wantRemoves, tc.fs.removes)
			for _, removed := range tc.wantRemoves {
				_, stillThere := tc.fs.files[removed]
				s.False(stillThere, "expected %s to be removed", removed)
			}
		})
	}
}

func (s *PatchSuite) TestApplyPatchWithNewlines() {
	patchText := `
*** Begin Patch
*** Update File: foo.txt
@@ foo
 foo
-bar
+bar updated
 haha
*** End of File
*** End Patch
`

	fs := newFakeFileSystem(map[string]string{
		"foo.txt": "foo\nbar\nhaha",
	})
	result, err := ApplyPatch(fs, patchText)
	s.Require().NoError(err)
	s.Equal("Done!", result)
	s.Equal("foo\nbar updated\nhaha", fs.files["foo.txt"])
}

func (s *PatchSuite) TestApplyPatchDemoAddTest() {
	// Original content of demo/add_test.go before the patch
	originalContent := `package demo

import (
	"fmt"
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

func TestAdd(t *testing.T) {
	tests := []struct {
		a, b, expected int
	}{
		{
			a:        1,
			b:        2,
			expected: 3,
		},
		{
			a:        1,
			b:        2,
			expected: 3,
		},
	}
	for _, test := range tests {
		t.Run(fmt.Sprintf("Add(%d, %d)", test.a, test.b), func(t *testing.T) {
			actual := Add(test.a, test.b)
			if actual != test.expected {
				t.Errorf("Add(%d, %d) = %d, expected %d", test.a, test.b, actual, test.expected)
			}
		})
	}
}

type AddTestSuite struct {
	suite.Suite
}

func (suite *AddTestSuite) TestAdd() {
	tests := []struct {
		a, b, expected int
	}{
		{
			a:        1,
			b:        2,
			expected: 3,
		},
		{
			a:        -1,
			b:        -2,
			expected: -3,
		},
	}
	for _, test := range tests {
		suite.Run(fmt.Sprintf("Add(%d, %d)", test.a, test.b), func() {
			actual := Add(test.a, test.b)
			assert.Equal(suite.T(), test.expected, actual, "Add(%d, %d) = %d, expected %d", test.a, test.b, actual, test.expected)
		})
	}
}

func (suite *AddTestSuite) TestSuperAdd() {
	tests := []struct {
		a, b, expected *big.Int
	}{
		{
			a:        big.NewInt(1),
			b:        big.NewInt(2),
			expected: big.NewInt(3),
		},
		{
			a:        big.NewInt(-1),
			b:        big.NewInt(-2),
			expected: big.NewInt(-3),
		},
	}
	for _, test := range tests {
		suite.Run(fmt.Sprintf("SuperAdd(%s, %s)", test.a, test.b), func() {
			actual := SuperAdd(test.a, test.b)
			assert.Equal(suite.T(), test.expected, actual, "SuperAdd(%s, %s) = %s, expected %s", test.a, test.b, actual, test.expected)
		})
	}
}

func TestAddTestSuite(t *testing.T) {
	suite.Run(t, new(AddTestSuite))
}
`

	// Expected content after applying the patch
	expectedContent := `package demo

import (
	"fmt"
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type AddTestSuite struct {
	suite.Suite
}

func (suite *AddTestSuite) TestAdd() {
	tests := []struct {
		a, b, expected int
	}{
		{
			a:        1,
			b:        2,
			expected: 3,
		},
		{
			a:        -1,
			b:        -2,
			expected: -3,
		},
	}
	for _, test := range tests {
		suite.Run(fmt.Sprintf("Add(%d, %d)", test.a, test.b), func() {
			actual := Add(test.a, test.b)
			assert.Equal(suite.T(), test.expected, actual, "Add(%d, %d) = %d, expected %d", test.a, test.b, actual, test.expected)
		})
	}
}

func (suite *AddTestSuite) TestSuperAdd() {
	tests := []struct {
		a, b, expected *big.Int
	}{
		{
			a:        big.NewInt(1),
			b:        big.NewInt(2),
			expected: big.NewInt(3),
		},
		{
			a:        big.NewInt(-1),
			b:        big.NewInt(-2),
			expected: big.NewInt(-3),
		},
	}
	for _, test := range tests {
		suite.Run(fmt.Sprintf("SuperAdd(%s, %s)", test.a, test.b), func() {
			actual := SuperAdd(test.a, test.b)
			assert.Equal(suite.T(), test.expected, actual, "SuperAdd(%s, %s) = %s, expected %s", test.a, test.b, actual, test.expected)
		})
	}
}

func TestAddTestSuite(t *testing.T) {
	suite.Run(t, new(AddTestSuite))
}
`

	// Patch text that replaces the entire file
	patchText := `*** Begin Patch
*** Update File: demo/add_test.go
 package demo
 
 import (
 	"fmt"
 	"math/big"
 	"testing"
 
 	"github.com/stretchr/testify/assert"
 	"github.com/stretchr/testify/suite"
 )
 
-func TestAdd(t *testing.T) {
-	tests := []struct {
-		a, b, expected int
-	}{
-		{
-			a:        1,
-			b:        2,
-			expected: 3,
-		},
-		{
-			a:        1,
-			b:        2,
-			expected: 3,
-		},
-	}
-	for _, test := range tests {
-		t.Run(fmt.Sprintf("Add(%d, %d)", test.a, test.b), func(t *testing.T) {
-			actual := Add(test.a, test.b)
-			if actual != test.expected {
-				t.Errorf("Add(%d, %d) = %d, expected %d", test.a, test.b, actual, test.expected)
-			}
-		})
-	}
-}
-
 type AddTestSuite struct {
 	suite.Suite
 }
 
 func (suite *AddTestSuite) TestAdd() {
 	tests := []struct {
 		a, b, expected int
 	}{
 		{
 			a:        1,
 			b:        2,
 			expected: 3,
 		},
 		{
 			a:        -1,
 			b:        -2,
 			expected: -3,
 		},
 	}
 	for _, test := range tests {
 		suite.Run(fmt.Sprintf("Add(%d, %d)", test.a, test.b), func() {
 			actual := Add(test.a, test.b)
 			assert.Equal(suite.T(), test.expected, actual, "Add(%d, %d) = %d, expected %d", test.a, test.b, actual, test.expected)
 		})
 	}
 }
 
 func (suite *AddTestSuite) TestSuperAdd() {
 	tests := []struct {
 		a, b, expected *big.Int
 	}{
 		{
 			a:        big.NewInt(1),
 			b:        big.NewInt(2),
 			expected: big.NewInt(3),
 		},
 		{
 			a:        big.NewInt(-1),
 			b:        big.NewInt(-2),
 			expected: big.NewInt(-3),
 		},
 	}
 	for _, test := range tests {
 		suite.Run(fmt.Sprintf("SuperAdd(%s, %s)", test.a, test.b), func() {
 			actual := SuperAdd(test.a, test.b)
 			assert.Equal(suite.T(), test.expected, actual, "SuperAdd(%s, %s) = %s, expected %s", test.a, test.b, actual, test.expected)
 		})
 	}
 }
 
 func TestAddTestSuite(t *testing.T) {
 	suite.Run(t, new(AddTestSuite))
 }
*** End Patch
`

	fs := newFakeFileSystem(map[string]string{
		"demo/add_test.go": originalContent,
	})

	result, err := ApplyPatch(fs, patchText)
	s.Require().NoError(err)
	s.Equal("Done!", result)
	s.Equal(expectedContent, fs.files["demo/add_test.go"])
}
