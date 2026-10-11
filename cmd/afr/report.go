package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/obijones/afr/internal/platform"
	"github.com/obijones/afr/internal/store"
	"github.com/obijones/afr/internal/view"
)

const reportUsage = "usage: afr report [-o FILE.html] [--since 7d|36h|2026-10-01] [session-id or prefix ...]\n"

func runReport(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	outPath := fs.String("o", "", "HTML file to write (default afr-report-<time>.html in the current folder)")
	since := fs.String("since", "", "only sessions active since then: a number of days (7d), hours (36h), or a date (2026-10-01)")
	if fs.Parse(args) != nil {
		return 1
	}
	now := time.Now()
	var cutoff time.Time
	if *since != "" {
		var err error
		if cutoff, err = parseSince(*since, now); err != nil {
			fmt.Fprintln(stderr, "afr:", err)
			fmt.Fprint(stderr, reportUsage)
			return 1
		}
	}
	dataDir, err := platform.DataDir()
	if err != nil {
		fmt.Fprintln(stderr, "afr:", err)
		return 1
	}

	var paths []string
	if fs.NArg() > 0 {
		for _, id := range fs.Args() {
			p, err := view.Resolve(dataDir, id)
			if err != nil {
				fmt.Fprintln(stderr, "afr:", err)
				return 1
			}
			paths = append(paths, p)
		}
	} else {
		rows, err := view.List(dataDir)
		if err != nil {
			fmt.Fprintln(stderr, "afr:", err)
			return 1
		}
		for _, r := range rows {
			paths = append(paths, store.LogPath(dataDir, r.Session))
		}
	}

	var sessions []view.ReportSession
	for _, p := range paths {
		events, err := view.Load(p)
		if err != nil || len(events) == 0 {
			continue
		}
		if !cutoff.IsZero() && view.Summarize(events).LastSeen.Before(cutoff) {
			continue
		}
		rs := view.ReportSession{Events: events, Integrity: "ok", OK: true, Current: view.Rescan(events, dataDir)}
		if probs, err := store.Verify(p); err != nil {
			rs.Integrity, rs.OK = "not checked ("+err.Error()+")", false
		} else if len(probs) > 0 {
			rs.Integrity, rs.OK = fmt.Sprintf("%d problems, run afr verify", len(probs)), false
		}
		sessions = append(sessions, rs)
	}
	if len(sessions) == 0 {
		fmt.Fprintln(stderr, "afr: no sessions to report in", dataDir)
		return 1
	}

	scope := "all sessions"
	switch {
	case fs.NArg() > 0:
		scope = "selected sessions"
	case *since != "":
		scope = "sessions active since " + cutoff.Format("2006-01-02 15:04")
	}
	out := *outPath
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if out == "" {
		out = "afr-report-" + now.Format("20060102-150405") + ".html"
		flags = os.O_WRONLY | os.O_CREATE | os.O_EXCL
	}
	f, err := os.OpenFile(out, flags, 0o600)
	if err != nil {
		fmt.Fprintln(stderr, "afr:", err)
		return 1
	}
	err = view.Report(f, sessions, view.ReportOptions{Version: version, Generated: now, Scope: scope})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(out)
		fmt.Fprintln(stderr, "afr:", err)
		return 1
	}
	n := fmt.Sprintf("%d sessions", len(sessions))
	if len(sessions) == 1 {
		n = "1 session"
	}
	fmt.Fprintf(stdout, "wrote %s (%s)\n", out, n)
	fmt.Fprintln(stdout, "it holds full prompts and tool output; share it only with people allowed to see them")
	return 0
}

// parseSince reads 7d, 36h, or a date (local midnight).
func parseSince(s string, now time.Time) (time.Time, error) {
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t, nil
	}
	if n, ok := strings.CutSuffix(s, "d"); ok {
		if d, err := strconv.Atoi(n); err == nil && d >= 0 {
			return now.AddDate(0, 0, -d), nil
		}
	}
	if d, err := time.ParseDuration(s); err == nil && d >= 0 {
		return now.Add(-d), nil
	}
	return time.Time{}, errors.New("--since: want a number of days (7d), a duration (36h), or a date (2026-10-01), got " + strconv.Quote(s))
}
