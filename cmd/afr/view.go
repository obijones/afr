package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/obijones/afr/internal/platform"
	"github.com/obijones/afr/internal/store"
	"github.com/obijones/afr/internal/view"
)

func runSessions(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("sessions", flag.ContinueOnError)
	fs.SetOutput(stderr)
	limit := fs.Int("n", 20, "show at most this many sessions, most recent first (0 = all)")
	if fs.Parse(args) != nil {
		return 1
	}
	dataDir, err := platform.DataDir()
	if err != nil {
		fmt.Fprintln(stderr, "afr:", err)
		return 1
	}
	rows, err := view.List(dataDir)
	if err != nil {
		fmt.Fprintln(stderr, "afr:", err)
		return 1
	}
	if *limit > 0 && len(rows) > *limit {
		rows = rows[:*limit]
	}
	if err := view.SessionTable(stdout, rows, nil); err != nil {
		return 1
	}
	return 0
}

func runReplay(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	fs.SetOutput(stderr)
	full := fs.Bool("full", false, "print complete prompts, replies, tool inputs, and outputs")
	width := fs.Int("width", 110, "cut summary lines to this many characters")
	if fs.Parse(args) != nil {
		return 1
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: afr replay [--full] [--width N] <session-id or unique prefix>")
		return 1
	}
	dataDir, err := platform.DataDir()
	if err != nil {
		fmt.Fprintln(stderr, "afr:", err)
		return 1
	}
	path, err := view.Resolve(dataDir, fs.Arg(0))
	if err != nil {
		fmt.Fprintln(stderr, "afr:", err)
		return 1
	}
	events, err := view.Load(path)
	if err != nil {
		fmt.Fprintln(stderr, "afr:", err)
		return 1
	}
	integrity := "ok"
	if probs, err := store.Verify(path); err != nil {
		integrity = "not checked (" + err.Error() + ")"
	} else if len(probs) > 0 {
		integrity = fmt.Sprintf("%d problems, run afr verify", len(probs))
	}
	if err := view.Replay(stdout, events, integrity, view.Options{Full: *full, Width: *width}); err != nil {
		fmt.Fprintln(stderr, "afr:", err)
		return 1
	}
	return 0
}
