package renderxml

import (
	"encoding/xml"
	"sort"
)

type FileXML struct {
	Path    string `xml:"path,attr"`
	Content string `xml:",chardata"`
}

type FilesXML struct {
	XMLName xml.Name  `xml:"Files"`
	Files   []FileXML `xml:"File"`
}

type StdioXML struct {
	Truncated bool   `xml:"truncated,attr"`
	Content   string `xml:",chardata"`
}

type LastRunXML struct {
	XMLName     xml.Name `xml:"LastRun"`
	Ran         bool     `xml:"ran,attr"`
	ExitCode    int      `xml:"exit_code,attr,omitempty"`
	DurationMs  int64    `xml:"duration_ms,attr,omitempty"`
	TimedOut    bool     `xml:"timed_out,attr,omitempty"`
	StartedAt   string   `xml:"started_at,attr,omitempty"`
	CompletedAt string   `xml:"completed_at,attr,omitempty"`

	Command string    `xml:"Command,omitempty"`
	Stdout  *StdioXML `xml:"Stdout,omitempty"`
	Stderr  *StdioXML `xml:"Stderr,omitempty"`
}

type ToolXML struct {
	Name    string `xml:"name,attr"`
	Command string `xml:"command,attr"`
	Desc    string `xml:"desc,attr,omitempty"`
}

type ToolsXML struct {
	XMLName xml.Name  `xml:"Tools"`
	Tools   []ToolXML `xml:"Tool"`
}

type ProjectStateXML struct {
	XMLName xml.Name   `xml:"ProjectState"`
	Files   FilesXML   `xml:"Files"`
	LastRun LastRunXML `xml:"LastRun"`
	Tools   ToolsXML   `xml:"Tools,omitempty"`
}

func BuildFilesXML(files map[string]string, filter []string, sanitize func(string) (string, error)) FilesXML {
	selected := make([]string, 0, len(files))
	if len(filter) == 0 {
		for path := range files {
			selected = append(selected, path)
		}
	} else {
		seen := make(map[string]struct{}, len(filter))
		for _, raw := range filter {
			canon, err := sanitize(raw)
			if err != nil {
				continue
			}
			if _, ok := files[canon]; !ok {
				continue
			}
			if _, exists := seen[canon]; exists {
				continue
			}
			seen[canon] = struct{}{}
			selected = append(selected, canon)
		}
	}
	sort.Strings(selected)

	list := make([]FileXML, 0, len(selected))
	for _, p := range selected {
		list = append(list, FileXML{Path: p, Content: files[p]})
	}
	return FilesXML{Files: list}
}
