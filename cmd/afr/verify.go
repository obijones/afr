package main

import (
	"fmt"
	"io"

	"github.com/obijones/afr/internal/platform"
	"github.com/obijones/afr/internal/store"
	"github.com/obijones/afr/internal/view"
)

func runVerify(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: afr verify <session-id or unique prefix>")
		return 1
	}
	dataDir, err := platform.DataDir()
	if err != nil {
		fmt.Fprintln(stderr, "afr:", err)
		return 1
	}
	path, err := view.Resolve(dataDir, args[0])
	if err != nil {
		fmt.Fprintln(stderr, "afr:", err)
		return 1
	}
	probs, err := store.Verify(path)
	if err != nil {
		fmt.Fprintln(stderr, "afr:", err)
		return 1
	}
	if len(probs) == 0 {
		fmt.Fprintf(stdout, "ok: %s\n", path)
		return 0
	}
	for _, p := range probs {
		fmt.Fprintf(stdout, "line %d: %s\n", p.Line, p.Detail)
	}
	return 1
}
