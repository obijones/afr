package main

import (
	"io"
	"strings"
	"testing"
)

func TestRunNeverReturnsTwo(t *testing.T) {
	for _, args := range [][]string{nil, {"bogus"}, {"Hook"}} {
		if code := run(args, strings.NewReader(""), io.Discard, io.Discard); code == 2 {
			t.Errorf("run(%q) = 2; exit 2 blocks PreToolUse in Claude Code", args)
		}
	}
}
