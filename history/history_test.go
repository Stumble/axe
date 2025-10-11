package history

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestChangelogAddLog(t *testing.T) {
	var changelog Changelog
	changelog.AddLog("first entry")
	changelog.AddLog("second entry")

	if len(changelog.Logs) != 2 {
		t.Fatalf("expected 2 log entries, got %d", len(changelog.Logs))
	}
	if changelog.Logs[0].Value != "first entry" {
		t.Fatalf("unexpected first log entry: %q", changelog.Logs[0].Value)
	}
	if changelog.Logs[1].Value != "second entry" {
		t.Fatalf("unexpected second log entry: %q", changelog.Logs[1].Value)
	}
}

func TestHistorySaveAndReadPreservesLogs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.xml")

	logText := "Agent line 1\nline 2 with <tags> & ampersands"

	hist := &History{FilePath: path}
	changelog := Changelog{Timestamp: time.Now(), Success: true}
	changelog.AddLog(logText)
	hist.AppendChangelog(changelog)

	if err := hist.SaveHistoryToFile(); err != nil {
		t.Fatalf("SaveHistoryToFile() error = %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	content := string(raw)
	if !strings.Contains(content, "<![CDATA["+logText+"]]") {
		t.Fatalf("expected CDATA-wrapped log entry, got %s", content)
	}

	loaded, err := ReadHistoryFromFile(path)
	if err != nil {
		t.Fatalf("ReadHistoryFromFile() error = %v", err)
	}

	if len(loaded.Changelogs) != 1 {
		t.Fatalf("expected 1 changelog, got %d", len(loaded.Changelogs))
	}

	logs := loaded.Changelogs[0].Logs
	if len(logs) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(logs))
	}
	if logs[0].Value != logText {
		t.Fatalf("expected log value %q, got %q", logText, logs[0].Value)
	}
}
