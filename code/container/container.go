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
// - CodeOutput: XML describing edits; v4a diff text format.
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

	"github.com/stumble/axe/code/v4a"
)

// CodeContainer holds an in-memory mapping of file paths to contents and offers
// helpers to render inputs, apply outputs, and persist to disk.
type CodeContainer struct {
	files   map[string]string
	deleted map[string]struct{}
}

// NewCodeContainer constructs a container with a copy of the provided files map.
func NewCodeContainer(files map[string]string) *CodeContainer {
	copy := make(map[string]string, len(files))
	for k, v := range files {
		copy[k] = v
	}
	return &CodeContainer{
		files:   copy,
		deleted: make(map[string]struct{}),
	}
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
		if _, ok := c.deleted[k]; ok {
			continue
		}
		out[k] = v
	}
	return out
}

// Clone returns a copy of the current container.
func (c *CodeContainer) Clone() CodeContainer {
	deleted := make(map[string]struct{}, len(c.deleted))
	for k := range c.deleted {
		deleted[k] = struct{}{}
	}
	return CodeContainer{
		files:   c.Files(),
		deleted: deleted,
	}
}

// BuildCodeInput renders a CodeInput for the selected paths (or all when empty).
func (c *CodeContainer) BuildCodeInput(filter []string) CodeInput {
	return BuildCodeInput(c.files, filter)
}

func (c *CodeContainer) Open(path string) (string, error) {
	if _, ok := c.deleted[path]; ok {
		return "", fmt.Errorf("code/container: file %s was deleted", path)
	}
	return c.files[path], nil
}

func (c *CodeContainer) Write(path, content string) error {
	c.files[path] = content
	delete(c.deleted, path)
	return nil
}

func (c *CodeContainer) Remove(path string) error {
	delete(c.files, path)
	c.deleted[path] = struct{}{}
	return nil
}

// Apply applies a CodeOutput to the container, mutating its files. Returns a message.
func (c *CodeContainer) Apply(output CodeOutput) (string, error) {
	return v4a.ApplyPatch(c, output.Patch)
}

// WriteToFiles applies all changes to the container to the file system.
func (c *CodeContainer) WriteToFiles() error {
	for f, c := range c.files {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			return fmt.Errorf("code/container: create dir for %s: %w", f, err)
		}
		mode := os.FileMode(0o600)
		if info, statErr := os.Stat(f); statErr == nil {
			mode = info.Mode()
		}
		if err := os.WriteFile(f, []byte(c), mode); err != nil {
			return fmt.Errorf("code/container: write %s: %w", f, err)
		}
	}
	for f := range c.deleted {
		if err := os.Remove(f); err != nil {
			return fmt.Errorf("code/container: remove %s: %w", f, err)
		}
	}
	return nil
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

// The Code Output context is xml struct for all updated code files. It is a simple v4a diff text format.
// Example:
// <CodeOutput version="first_draft">
// *** Begin Patch
// *** Update File: notes.txt
// @@ hello
// -hello
// +hi
//
//	world
//
// *** End Patch
// </CodeOutput>
type CodeOutput struct {
	XMLName xml.Name `xml:"CodeOutput"`
	Version string   `xml:"version,attr,omitempty"`
	Patch   string   `xml:",chardata"`
}

// ParseCodeOutput parses a CodeOutput XML payload.
func ParseCodeOutput(xmlPayload string) (CodeOutput, error) {
	var out CodeOutput
	if err := xml.Unmarshal([]byte(strings.TrimSpace(xmlPayload)), &out); err != nil {
		return CodeOutput{}, fmt.Errorf("code/container: parse CodeOutput: %w", err)
	}
	return out, nil
}
