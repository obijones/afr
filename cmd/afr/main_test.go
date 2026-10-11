package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
