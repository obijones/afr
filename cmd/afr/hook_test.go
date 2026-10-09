package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestHookSavesPayload(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AFR_HOME", home)
	payload := `{"session_id":"s1","hook_event_name":"UserPromptSubmit","prompt":"hi"}`
	if code := runHook(strings.NewReader(payload)); code != 0 {
		t.Fatalf("runHook = %d, want 0", code)
	}
	files, _ := filepath.Glob(filepath.Join(home, "raw", "s1", "*-UserPromptSubmit.json"))
	if len(files) != 1 {
		t.Fatalf("found %d raw files, want 1", len(files))
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
