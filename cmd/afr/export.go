package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/obijones/afr/internal/coverage"
	"github.com/obijones/afr/internal/export"
	"github.com/obijones/afr/internal/platform"
	"github.com/obijones/afr/internal/store"
	"github.com/obijones/afr/internal/view"
)

const exportUsage = "usage: afr export [-o FILE.zip] [--transcripts DIR | --no-coverage] <session-id or prefix> [...]\n       afr export [-o FILE.zip] [--transcripts DIR | --no-coverage] --last N\n"

func runExport(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	outPath := fs.String("o", "", "zip file to write (default afr-export-<time>.zip in the current folder)")
	last := fs.Int("last", 0, "export the N most recently active sessions")
	transcripts := fs.String("transcripts", "", "Claude Code projects folder to check coverage against (default: found from afr's sessions)")
	noCoverage := fs.Bool("no-coverage", false, "do not add coverage.json")
	if fs.Parse(args) != nil {
		return 1
	}
	if (fs.NArg() == 0) == (*last <= 0) {
		fmt.Fprint(stderr, exportUsage)
		return 1
	}
	dataDir, err := platform.DataDir()
	if err != nil {
		fmt.Fprintln(stderr, "afr:", err)
		return 1
	}

	var paths []string
	if *last > 0 {
		rows, err := view.List(dataDir)
		if err != nil {
			fmt.Fprintln(stderr, "afr:", err)
			return 1
		}
		if len(rows) == 0 {
			fmt.Fprintln(stderr, "afr: no sessions recorded in", dataDir)
			return 1
		}
		for _, r := range rows[:min(*last, len(rows))] {
			paths = append(paths, store.LogPath(dataDir, r.Session))
		}
	} else {
		for _, id := range fs.Args() {
			p, err := view.Resolve(dataDir, id)
			if err != nil {
				fmt.Fprintln(stderr, "afr:", err)
				return 1
			}
			paths = append(paths, p)
		}
	}

	now := time.Now()
	out := *outPath
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if out == "" {
		out = "afr-export-" + now.Format("20060102-150405") + ".zip"
		flags = os.O_WRONLY | os.O_CREATE | os.O_EXCL
	}
	f, err := os.OpenFile(out, flags, 0o600)
	if err != nil {
		fmt.Fprintln(stderr, "afr:", err)
		return 1
	}
	opts := export.Options{Version: version, Now: now}
	if !*noCoverage {
		opts.Coverage, opts.CoverageDir = exportCoverage(dataDir, *transcripts, paths, *last > 0, stderr)
	}
	m, err := export.Write(f, dataDir, paths, opts)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(out)
		fmt.Fprintln(stderr, "afr:", err)
		return 1
	}

	for _, s := range m.Sessions {
		fmt.Fprintf(stdout, "%s  %s, %d events, %d raw payloads, integrity %s\n",
			s.Session, s.Status, s.Events, s.RawFiles, s.Integrity)
		if s.Status == "open" {
			fmt.Fprintf(stdout, "  note: session %.8s is still open; end it (/exit) and export again for the complete record\n", s.Session)
		}
	}
	fmt.Fprintln(stdout, "wrote", out)
	fmt.Fprintln(stdout, "it holds full prompts and tool output; share it only with people allowed to see them")
	return 0
}

// exportCoverage checks the transcripts changed since the earliest exported
// session started. It keeps the exported sessions' results and, for
// --last, conversations afr has no log of.
func exportCoverage(dataDir, dir string, paths []string, listMissed bool, stderr io.Writer) ([]coverage.Result, string) {
	chosen := map[string]bool{}
	var since time.Time
	for _, p := range paths {
		evs, err := view.Load(p)
		if err != nil || len(evs) == 0 {
			continue
		}
		s := view.Summarize(evs)
		chosen[s.Session] = true
		if since.IsZero() || s.Started.Before(since) {
			since = s.Started
		}
	}
	results, used, err := checkCoverage(dataDir, dir, since)
	if err == nil {
		_, err = os.Stat(used)
	}
	if err != nil {
		fmt.Fprintln(stderr, "afr: coverage not checked:", err)
		return nil, ""
	}
	keep := []coverage.Result{}
	for _, r := range results {
		if chosen[r.Session] || (listMissed && r.Status == coverage.Missed) {
			keep = append(keep, r)
		}
	}
	return keep, used
}
