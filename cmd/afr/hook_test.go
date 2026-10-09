package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/obijones/afr/internal/store"
)

func TestHookWritesEvents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AFR_HOME", home)
	t.Setenv("AFR_CAPTURE_RAW", "")
	payload, err := os.ReadFile("../../testdata/claude-code/2.1.295/Stop.json")
	if err != nil {
		t.Fatal(err)
	}
	if code := runHook(bytes.NewReader(payload)); code != 0 {
		t.Fatalf("runHook = %d, want 0", code)
	}
	logs, _ := filepath.Glob(filepath.Join(home, "sessions", "*.ndjson"))
	if len(logs) != 1 {
		t.Fatalf("found %d session logs, want 1", len(logs))
	}
	b, _ := os.ReadFile(logs[0])
	if n := strings.Count(string(b), "\n"); n != 2 {
		t.Fatalf("Stop wrote %d events, want 2 (reply and turn end)", n)
	}
	if raw, _ := filepath.Glob(filepath.Join(home, "raw", "*", "*")); len(raw) != 0 {
		t.Fatalf("raw payload saved without AFR_CAPTURE_RAW: %v", raw)
	}
}

func TestHookRawCaptureOptIn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AFR_HOME", home)
	t.Setenv("AFR_CAPTURE_RAW", "1")
	runHook(strings.NewReader(`{"session_id":"s1","hook_event_name":"UserPromptSubmit","prompt":"hi"}`))
	if raw, _ := filepath.Glob(filepath.Join(home, "raw", "s1", "*-UserPromptSubmit.json")); len(raw) != 1 {
		t.Fatalf("found %d raw files, want 1", len(raw))
	}
}

func TestHookUnrecognizedPayloadKeptRaw(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AFR_HOME", home)
	t.Setenv("AFR_CAPTURE_RAW", "")
	if code := runHook(strings.NewReader(`{"sessionId":"c1","toolName":"bash"}`)); code != 0 {
		t.Fatalf("runHook = %d, want 0", code)
	}
	if raw, _ := filepath.Glob(filepath.Join(home, "raw", "*", "*")); len(raw) != 1 {
		t.Fatalf("unrecognized payload not kept raw: %v", raw)
	}
	if log, _ := os.ReadFile(filepath.Join(home, "afr-errors.log")); !bytes.Contains(log, []byte("convert payload")) {
		t.Fatalf("error log = %q", log)
	}
}

func TestHookPanicReturnsZero(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AFR_HOME", home)
	t.Setenv("AFR_TEST_PANIC", "1")
	if code := runHook(strings.NewReader("{}")); code != 0 {
		t.Fatalf("runHook after panic = %d, want 0", code)
	}
	log, err := os.ReadFile(filepath.Join(home, "afr-errors.log"))
	if err != nil || !bytes.Contains(log, []byte("forced panic")) {
		t.Fatalf("error log missing the panic: %q, %v", log, err)
	}
}

func TestHookStdinTimeout(t *testing.T) {
	t.Setenv("AFR_HOME", t.TempDir())
	old := stdinTimeout
	stdinTimeout = 50 * time.Millisecond
	defer func() { stdinTimeout = old }()

	r, w := io.Pipe() // never written to or closed
	defer w.Close()
	start := time.Now()
	if code := runHook(r); code != 0 {
		t.Fatalf("runHook = %d, want 0", code)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("runHook took %v, want it to give up near the timeout", elapsed)
	}
}

// TestBinaryExitCode checks the real process, since an unrecovered Go
// panic exits with status 2, which Claude Code treats as a block.
func TestBinaryExitCode(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := filepath.Join(t.TempDir(), "afr")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	for _, panicEnv := range []string{"0", "1"} {
		cmd := exec.Command(bin, "hook")
		cmd.Env = append(os.Environ(), "AFR_HOME="+t.TempDir(), "AFR_TEST_PANIC="+panicEnv)
		cmd.Stdin = strings.NewReader(`{"session_id":"s1","hook_event_name":"PreToolUse"}`)
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		if err := cmd.Run(); err != nil {
			t.Fatalf("AFR_TEST_PANIC=%s: afr hook failed: %v", panicEnv, err)
		}
		if stdout.Len() != 0 {
			t.Fatalf("AFR_TEST_PANIC=%s: afr hook wrote to stdout: %q", panicEnv, stdout.String())
		}
	}
}

func TestHookLockTimeoutKeepsRaw(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AFR_HOME", home)
	t.Setenv("AFR_CAPTURE_RAW", "")
	old := store.LockTimeout
	store.LockTimeout = 100 * time.Millisecond
	defer func() { store.LockTimeout = old }()

	payload, _ := os.ReadFile("../../testdata/claude-code/2.1.295/UserPromptSubmit.json")
	var p struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(payload, &p)
	_ = os.MkdirAll(store.SessionsDir(home), 0o700)
	holder := flock.New(store.LogPath(home, p.SessionID) + ".lock")
	if err := holder.Lock(); err != nil {
		t.Fatal(err)
	}
	defer holder.Unlock()

	start := time.Now()
	if code := runHook(bytes.NewReader(payload)); code != 0 {
		t.Fatalf("runHook = %d, want 0", code)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("runHook waited %v on a held lock", elapsed)
	}
	if raw, _ := filepath.Glob(filepath.Join(home, "raw", "*", "*")); len(raw) != 1 {
		t.Fatalf("payload not kept raw after lock timeout: %v", raw)
	}
}
