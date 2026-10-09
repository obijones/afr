package collect

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSaveRawKeepsBytes(t *testing.T) {
	dir := t.TempDir()
	payload := []byte(`{"session_id":"abc123","hook_event_name":"PreToolUse","tool_name":"Bash"}`)
	path, err := SaveRaw(dir, payload, time.Unix(1, 5))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "raw", "abc123"); filepath.Dir(path) != want {
		t.Fatalf("saved in %q, want %q", filepath.Dir(path), want)
	}
	if !strings.HasSuffix(path, "-PreToolUse.json") {
		t.Fatalf("file name %q does not end with the event name", path)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("saved %q, want the payload unchanged", got)
	}
}

func TestSaveRawInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path, err := SaveRaw(dir, []byte("not json"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(path, filepath.Join("raw", "unknown-session")) {
		t.Fatalf("path %q, want it under raw/unknown-session", path)
	}
}
