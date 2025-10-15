package history

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Changelog struct {
	Timestamp time.Time  `xml:"Timestamp"`
	Success   bool       `xml:"Success"`
	Logs      []LogEntry `xml:"Logs>Log"`
	TODO      string     `xml:"TODO"`
}

type LogEntry struct {
	Value string `xml:",chardata"`
}

func (l LogEntry) MarshalXML(e *xml.Encoder, start xml.StartElement) error {
	type cdataWrapper struct {
		Text string `xml:",cdata"`
	}
	return e.EncodeElement(cdataWrapper{Text: l.Value}, start)
}

func (c *Changelog) AddLog(entry string) {
	if c == nil {
		return
	}
	c.Logs = append(c.Logs, LogEntry{Value: entry})
}

type History struct {
	XMLName    xml.Name    `xml:"History"`
	Changelogs []Changelog `xml:"Changelogs>Changelog"`
	FilePath   string      `xml:"-"`
}

func (h *History) AppendChangelog(changelog Changelog) {
	if h == nil {
		return
	}
	h.Changelogs = append(h.Changelogs, changelog)
}

func ReadHistoryFromFile(path string) (*History, error) {
	path = strings.TrimSpace(path)
	hist := &History{FilePath: path}
	if path == "" {
		return hist, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return hist, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return hist, nil
	}
	if err := xml.Unmarshal(data, hist); err != nil {
		return nil, err
	}
	// Preserve file path on loaded struct
	hist.FilePath = path
	return hist, nil
}

func (h *History) SaveHistoryToFile() error {
	path := strings.TrimSpace(h.FilePath)
	if path == "" {
		return nil
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	buf, err := xml.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	content := append([]byte(xml.Header), buf...)
	return os.WriteFile(path, content, 0o600)
}

func (h *History) LastChangelogTimestamp() (time.Time, bool) {
	if len(h.Changelogs) == 0 {
		return time.Time{}, false
	}
	return h.Changelogs[len(h.Changelogs)-1].Timestamp, true
}
