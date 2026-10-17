package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestRunNeverReturnsTwo(t *testing.T) {
	for _, args := range [][]string{nil, {"bogus"}, {"Hook"}} {
		if code := run(args, strings.NewReader(""), io.Discard, io.Discard); code == 2 {
			t.Errorf("run(%q) = 2; exit 2 blocks PreToolUse in Claude Code", args)
		}
	}
}

func TestReplayToFileAndVerifyByPrefix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AFR_HOME", home)
	payload, err := os.ReadFile("../../testdata/claude-code/2.1.296-windows/UserPromptSubmit.json")
	if err != nil {
		t.Fatal(err)
	}
	runHook(bytes.NewReader(payload))

	var out, errb bytes.Buffer
	if code := run([]string{"verify", "a6ac"}, nil, &out, &errb); code != 0 {
		t.Fatalf("verify by prefix: %d %s", code, errb.String())
	}
	file := filepath.Join(home, "replay.txt")
	if code := run([]string{"replay", "-o", file, "a6ac"}, nil, &out, &errb); code != 0 {
		t.Fatalf("replay -o: %d %s", code, errb.String())
	}
	b, err := os.ReadFile(file)
	if err != nil || !strings.Contains(string(b), "SESSION a6ac7efa") || !utf8.Valid(b) {
		t.Fatalf("replay file: %v %q", err, b)
	}
}

func TestExport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AFR_HOME", home)
	t.Setenv("HOME", home) // coverage falls back to ~/.claude/projects: keep it off the real one
	t.Setenv("USERPROFILE", home)
	t.Setenv("AFR_CAPTURE_RAW", "1")
	for _, name := range []string{"SessionStart-startup.json", "UserPromptSubmit.json"} {
		payload, err := os.ReadFile("../../testdata/claude-code/2.1.296-windows/" + name)
		if err != nil {
			t.Fatal(err)
		}
		runHook(bytes.NewReader(payload))
	}
	zipPath := filepath.Join(t.TempDir(), "out.zip")
	var out, errb bytes.Buffer
	if code := run([]string{"export", "-o", zipPath, "a6ac"}, nil, &out, &errb); code != 0 {
		t.Fatalf("export: %d %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "still open") || !strings.Contains(out.String(), "integrity ok") {
		t.Errorf("export output: %s", out.String())
	}

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	names := map[string]*zip.File{}
	raw := 0
	for _, f := range zr.File {
		if strings.Contains(f.Name, `\`) {
			t.Errorf("entry %q has a backslash; unzip on Linux would make a flat file", f.Name)
		}
		if strings.HasPrefix(f.Name, "raw/a6ac7efa-4d80-4eee-8ffa-6159bda964ed/") {
			raw++
		}
		names[f.Name] = f
	}
	id := "a6ac7efa-4d80-4eee-8ffa-6159bda964ed"
	for _, want := range []string{"sessions/" + id + ".ndjson", "sessions/" + id + ".state.json", "replay-a6ac7efa.txt", "manifest.json"} {
		if names[want] == nil {
			t.Errorf("missing %s in %v", want, names)
		}
	}
	if names["sessions/"+id+".ndjson.lock"] != nil {
		t.Error("lock file exported")
	}
	if raw != 2 {
		t.Errorf("raw payloads = %d, want 2", raw)
	}

	// The manifest hash matches the exported log.
	var m struct {
		Sessions []struct{ Status, Integrity string }
		Files    []struct{ Path, SHA256 string }
	}
	readZip := func(name string) []byte {
		rc, err := names[name].Open()
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		b, _ := io.ReadAll(rc)
		return b
	}
	if err := json.Unmarshal(readZip("manifest.json"), &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Sessions) != 1 || m.Sessions[0].Status != "open" || m.Sessions[0].Integrity != "ok" {
		t.Errorf("manifest sessions: %+v", m.Sessions)
	}
	sum := sha256.Sum256(readZip("sessions/" + id + ".ndjson"))
	found := false
	for _, f := range m.Files {
		if f.Path == "sessions/"+id+".ndjson" {
			found = f.SHA256 == hex.EncodeToString(sum[:])
		}
	}
	if !found {
		t.Error("manifest hash for the log missing or wrong")
	}
	if !strings.Contains(string(readZip("replay-a6ac7efa.txt")), "SESSION a6ac7efa") {
		t.Error("replay missing session header")
	}

	// --last picks the session too; the default name never overwrites.
	if code := run([]string{"export", "--last", "1", "-o", zipPath}, nil, &out, &errb); code != 0 {
		t.Fatalf("export --last: %d %s", code, errb.String())
	}
	if code := run([]string{"export"}, nil, &out, &errb); code == 0 || code == 2 {
		t.Errorf("export with no sessions named: %d", code)
	}
}

func TestReport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AFR_HOME", home)
	t.Setenv("HOME", home) // coverage falls back to ~/.claude/projects: keep it off the real one
	t.Setenv("USERPROFILE", home)
	payload, err := os.ReadFile("../../testdata/claude-code/2.1.296-windows/UserPromptSubmit.json")
	if err != nil {
		t.Fatal(err)
	}
	runHook(bytes.NewReader(payload))
	file := filepath.Join(t.TempDir(), "r.html")
	var out, errb bytes.Buffer
	if code := run([]string{"report", "-o", file}, nil, &out, &errb); code != 0 {
		t.Fatalf("report: %d %s", code, errb.String())
	}
	b, err := os.ReadFile(file)
	if err != nil || !strings.Contains(string(b), "a6ac7efa-4d80-4eee-8ffa-6159bda964ed") || !strings.Contains(out.String(), "1 session") {
		t.Fatalf("report output: %v %s", err, out.String())
	}
	if fi, _ := os.Stat(file); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("report mode %v, want 0600", fi.Mode().Perm())
	}
	// A cutoff after the session leaves nothing to report.
	if code := run([]string{"report", "-o", file, "--since", "2099-01-01"}, nil, &out, &errb); code != 1 {
		t.Errorf("report with nothing selected: %d", code)
	}
	if code := run([]string{"report", "--since", "soon"}, nil, &out, &errb); code != 1 {
		t.Errorf("bad --since: %d", code)
	}
}

// afr coverage compares transcripts with afr's logs: a prompt afr recorded,
// one it missed, and a conversation it never saw.
func TestCoverage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AFR_HOME", home)
	payload, err := os.ReadFile("../../testdata/claude-code/2.1.296-windows/UserPromptSubmit.json")
	if err != nil {
		t.Fatal(err)
	}
	runHook(bytes.NewReader(payload))

	projects := t.TempDir()
	now := time.Now().UTC()
	line := func(sec int, prompt, text string) string {
		b, _ := json.Marshal(map[string]any{"type": "user", "promptId": prompt, "timestamp": now.Add(time.Duration(sec) * time.Second),
			"cwd": `C:\work`, "entrypoint": "cli", "message": map[string]any{"role": "user", "content": text}})
		return string(b) + "\n"
	}
	write := func(session string, lines ...string) {
		p := filepath.Join(projects, "C--work", session+".jsonl")
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(strings.Join(lines, "")), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a6ac7efa-4d80-4eee-8ffa-6159bda964ed", line(0, "04bdc5fc-70c7-418b-922e-d1d0f7bcb2a2", "recorded"), line(60, "p2", "typed with afr's hooks removed"))
	write("b7000000-0000-0000-0000-000000000000", line(0, "p9", "never recorded"))

	var out, errb bytes.Buffer
	if code := run([]string{"coverage", "--transcripts", projects}, nil, &out, &errb); code != 0 {
		t.Fatalf("coverage: %d %s", code, errb.String())
	}
	for _, want := range []string{"partial", "missed", "not recorded: typed prompt", "a6ac7efa", "b7000000"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("coverage output missing %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	if code := run([]string{"coverage", "--transcripts", projects, "--json"}, nil, &out, &errb); code != 0 {
		t.Fatalf("coverage --json: %d %s", code, errb.String())
	}
	var results []map[string]any
	if err := json.Unmarshal(out.Bytes(), &results); err != nil || len(results) != 2 {
		t.Fatalf("coverage --json: %v %s", err, out.String())
	}

	// The export carries the same results, so a reviewer elsewhere sees them.
	zipPath := filepath.Join(t.TempDir(), "x.zip")
	if code := run([]string{"export", "-o", zipPath, "--transcripts", projects, "--last", "1"}, nil, &out, &errb); code != 0 {
		t.Fatalf("export: %d %s", code, errb.String())
	}
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var cov struct {
		Results []struct{ Session, Status string }
	}
	for _, f := range zr.File {
		if f.Name == "coverage.json" {
			rc, _ := f.Open()
			err = json.NewDecoder(rc).Decode(&cov)
			rc.Close()
		}
	}
	if err != nil || len(cov.Results) != 2 {
		t.Errorf("coverage.json in export: %v %+v", err, cov)
	}
}

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.Local)
	for in, want := range map[string]time.Time{
		"7d":         now.AddDate(0, 0, -7),
		"36h":        now.Add(-36 * time.Hour),
		"2026-10-01": time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local),
	} {
		got, err := parseSince(in, now)
		if err != nil || !got.Equal(want) {
			t.Errorf("parseSince(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "-3d", "x", "-1h"} {
		if _, err := parseSince(bad, now); err == nil {
			t.Errorf("parseSince(%q) accepted", bad)
		}
	}
}
