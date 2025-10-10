package container

// Design: Code flow for editing source files with LLM assistance
//
//   file system -> load to -> CodeContainer -> CodeInput (XML) -> LLM -> CodeOutput (XML)
//         -> apply to -> CodeContainer -> write back to -> file system
//
// Definitions
// - CodeContainer: in-memory view of files (path -> content). Acts as the single
//   source of truth during an editing session.
// - CodeInput: XML serialization of selected files, with file contents wrapped
//   in CDATA to preserve exact text.
// - CodeOutput: XML describing edits; edits are either <Rewrite> (replace full
//   content) or <ApplyDiff> (apply a patch via code/diff).
//
// Typical usage
// 1) Load files from the file system into a CodeContainer.
// 2) Render the container as CodeInput and send to the LLM.
// 3) Parse the LLM's CodeOutput and apply it to the container.
// 4) Persist the changed files from the container back to disk.

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	codiff "github.com/stumble/axe/code/diff"
)

// CodeContainer holds an in-memory mapping of file paths to contents and offers
// helpers to render inputs, apply outputs, and persist to disk.
type CodeContainer struct {
	files map[string]string
}

// NewCodeContainer constructs a container with a copy of the provided files map.
func NewCodeContainer(files map[string]string) *CodeContainer {
	copy := make(map[string]string, len(files))
	for k, v := range files {
		copy[k] = v
	}
	return &CodeContainer{files: copy}
}

// MustNewCodeContainerFromFS is a helper that panics if NewCodeContainerFromFS fails.
func MustNewCodeContainerFromFS(baseDir string, paths []string) *CodeContainer {
	cc, err := NewCodeContainerFromFS(baseDir, paths)
	if err != nil {
		panic(err)
	}
	return cc
}

// NewCodeContainerFromFS reads given paths from baseDir (or absolute) into a container.
func NewCodeContainerFromFS(baseDir string, paths []string) (*CodeContainer, error) {
	files := make(map[string]string, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		full := p
		if baseDir != "" && !filepath.IsAbs(p) {
			full = filepath.Join(baseDir, p)
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return nil, fmt.Errorf("code/context: read %s: %w", p, err)
		}
		files[full] = string(data)
	}
	return NewCodeContainer(files), nil
}

// Files returns a copy of the current in-memory files map.
func (c *CodeContainer) Files() map[string]string {
	out := make(map[string]string, len(c.files))
	for k, v := range c.files {
		out[k] = v
	}
	return out
}

// Clone returns a copy of the current container.
func (c *CodeContainer) Clone() CodeContainer {
	return CodeContainer{files: c.Files()}
}

// BuildCodeInput renders a CodeInput for the selected paths (or all when empty).
func (c *CodeContainer) BuildCodeInput(filter []string) CodeInput {
	return BuildCodeInput(c.files, filter)
}

// Apply applies a CodeOutput to the container, mutating its files. Returns changed paths.
func (c *CodeContainer) Apply(output CodeOutput) ([]string, error) {
	updated, changed, err := ApplyEdits(c.files, output)
	if err != nil {
		return nil, err
	}
	for _, p := range changed {
		c.files[p] = updated[p]
	}
	return changed, nil
}

// WriteToFiles writes the given paths (or all if empty) from the container.
func (c *CodeContainer) WriteToFiles(paths []string) ([]string, error) {
	toWrite := paths
	if len(toWrite) == 0 {
		toWrite = make([]string, 0, len(c.files))
		for p := range c.files {
			toWrite = append(toWrite, p)
		}
		sort.Strings(toWrite)
	}
	for _, p := range toWrite {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return nil, fmt.Errorf("code/context: create dir for %s: %w", p, err)
		}
		mode := os.FileMode(0o644)
		if info, statErr := os.Stat(p); statErr == nil {
			mode = info.Mode()
		}
		if err := os.WriteFile(p, []byte(c.files[p]), mode); err != nil {
			return nil, fmt.Errorf("code/context: write %s: %w", p, err)
		}
	}
	return toWrite, nil
}

// The Code Input context is xml struct of all code files. Codes are wrapped with <![CDATA[ and ]]> to avoid xml escaping.
// Example:
// <CodeInput>
//
//	<File path="main.go"><![CDATA[
//	  package main
//	  func main() {
//	    fmt.Println("Hello, World!")
//	  }
//	]]></File>
//	<File path="main_test.go"><![CDATA[
//	  ...
//	]]></File>
//
// </CodeInput>
type CodeInput struct {
	XMLName xml.Name   `xml:"CodeInput"`
	Files   []CodeFile `xml:"File"`
}

type CodeFile struct {
	Path    string `xml:"path,attr"`
	Content string `xml:"-"`
}

// BuildCodeInput builds a CodeInput document from the provided files map.
// The order of files is deterministic (sorted by path). If filter is provided,
// only those paths (that exist in files) are included.
func BuildCodeInput(files map[string]string, filter []string) CodeInput {
	selected := make([]string, 0, len(files))
	if len(filter) == 0 {
		for p := range files {
			selected = append(selected, p)
		}
	} else {
		seen := make(map[string]struct{}, len(filter))
		for _, p := range filter {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if _, ok := files[p]; !ok {
				continue
			}
			if _, exists := seen[p]; exists {
				continue
			}
			seen[p] = struct{}{}
			selected = append(selected, p)
		}
	}
	sort.Strings(selected)

	out := CodeInput{Files: make([]CodeFile, 0, len(selected))}
	for _, p := range selected {
		out.Files = append(out.Files, CodeFile{Path: p, Content: files[p]})
	}
	return out
}

// ToXML renders this CodeInput as an XML string with CDATA sections for file contents.
// We write CDATA blocks explicitly to preserve content verbatim.
func (ci CodeInput) ToXML() (string, error) {
	out, err := xml.MarshalIndent(ci, "", "  ")
	return string(out), err
}

// MarshalXML customizes File serialization to wrap content in CDATA
func (f CodeFile) MarshalXML(e *xml.Encoder, start xml.StartElement) error {
	type inner struct {
		XMLName xml.Name `xml:"File"`
		Path    string   `xml:"path,attr"`
		// Inject raw CDATA using innerxml
		Data string `xml:",innerxml"`
	}
	safe := strings.ReplaceAll(f.Content, "]]>", "]]]]><![CDATA[>")
	payload := inner{Path: f.Path, Data: "<![CDATA[" + safe + "]]" + ">"}
	return e.EncodeElement(payload, xml.StartElement{Name: xml.Name{Local: "File"}})
}

// The Code Output context is xml struct for all updated code files. Code can be edited by either complete rewrite (the whole file will be replaced) or partial rewrite (the file will be partially replaced).
// NOTE: For now, we don't allow file deletion. You can only clear the content of the file.
// Example:
// <CodeOutput version="first_draft">
//
//		 <ApplyDiff path="main.go"><![CDATA[
//		package main
//		func main() {
//	  - fmt.Println("Hello, World!")
//	  - fmt.Println("new content")
//	    }
//	    ]]></ApplyDiff>
//	    <ApplyDiff path="main_test.go"><![CDATA[
//	    ...
//	    ]]></ApplyDiff>
//	    <Rewrite path="some_other_file.go"><![CDATA[
//	    package some_other_file
//	    func some_other_file() {
//	    fmt.Println("new content")
//	    }
//	    ]]></Rewrite>
//
// </CodeOutput>
type CodeOutput struct {
	XMLName    xml.Name            `xml:"CodeOutput"`
	Version    string              `xml:"version,attr,omitempty"`
	ApplyDiffs []CodeOutputDiff    `xml:"ApplyDiff"`
	Rewrites   []CodeOutputRewrite `xml:"Rewrite"`
}

type CodeOutputDiff struct {
	Path string `xml:"path,attr"`
	// Patch content (diff hunk format defined by code/diff package)
	Patch string `xml:",chardata"`
}

type CodeOutputRewrite struct {
	Path    string `xml:"path,attr"`
	Content string `xml:",chardata"`
}

// ParseCodeOutput parses a CodeOutput XML payload.
func ParseCodeOutput(xmlPayload string) (CodeOutput, error) {
	var out CodeOutput
	if err := xml.Unmarshal([]byte(strings.TrimSpace(xmlPayload)), &out); err != nil {
		return CodeOutput{}, fmt.Errorf("code/context: parse CodeOutput: %w", err)
	}
	return out, nil
}

// ApplyEdits applies the edits described by a CodeOutput payload to the provided files map.
// - Rewrite replaces the entire file content
// - ApplyDiff applies a patch to the existing file content using the diff utility
// Returns a new files map and the list of paths that changed (sorted, unique).
func ApplyEdits(files map[string]string, output CodeOutput) (map[string]string, []string, error) {
	updated := make(map[string]string, len(files))
	for k, v := range files {
		updated[k] = v
	}

	changedSet := make(map[string]struct{})

	// Apply rewrites first (they can be overwritten by diffs if both present)
	for _, rw := range output.Rewrites {
		p := strings.TrimSpace(rw.Path)
		if p == "" {
			continue
		}
		// Rewrite can clear content (empty string), but not delete the file.
		updated[p] = rw.Content
		changedSet[p] = struct{}{}
	}

	// Apply diffs
	for _, df := range output.ApplyDiffs {
		p := strings.TrimSpace(df.Path)
		if p == "" {
			continue
		}
		orig := updated[p]
		patched, err := codiff.ApplyPatch(orig, df.Patch)
		if err != nil {
			return nil, nil, fmt.Errorf("code/context: apply diff to %s: %w", p, err)
		}
		updated[p] = patched
		changedSet[p] = struct{}{}
	}

	// Collect changed paths deterministically
	changed := make([]string, 0, len(changedSet))
	for p := range changedSet {
		changed = append(changed, p)
	}
	sort.Strings(changed)
	return updated, changed, nil
}
