package main

import (
	"flag"
	"fmt"
	"io"
	"os"

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
	outPath := fs.String("o", "", "write the replay to this file (UTF-8) instead of the terminal")
	if fs.Parse(args) != nil {
		return 1
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: afr replay [--full] [--width N] [-o FILE] <session-id or unique prefix>")
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
	o := view.Options{Full: *full, Width: *width, Current: view.Rescan(events, dataDir)}
	w := stdout
	var f *os.File
	if *outPath != "" {
		// afr writes the file itself, as UTF-8. Piping through Windows
		// PowerShell 5.1 re-decodes the text with the console code page
		// and garbles anything outside ASCII.
		f, err = os.OpenFile(*outPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			fmt.Fprintln(stderr, "afr:", err)
			return 1
		}
		w = f
	}
	err = view.Replay(w, events, integrity, o)
	if f != nil {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, "afr:", err)
		return 1
	}
	if f != nil {
		fmt.Fprintln(stdout, "wrote", *outPath)
	}
	return 0
}
