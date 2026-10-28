package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/obijones/afr/internal/coverage"
	"github.com/obijones/afr/internal/platform"
	"github.com/obijones/afr/internal/store"
	"github.com/obijones/afr/internal/view"
)

const coverageUsage = "usage: afr coverage [--since 7d|36h|2026-10-01] [--transcripts DIR] [--json]\n"

func runCoverage(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("coverage", flag.ContinueOnError)
	fs.SetOutput(stderr)
	since := fs.String("since", "7d", "only transcripts changed since then: days (7d), hours (36h), or a date")
	dir := fs.String("transcripts", "", "Claude Code projects folder (default: found from afr's sessions, else ~/.claude/projects)")
	asJSON := fs.Bool("json", false, "print the results as JSON")
	if fs.Parse(args) != nil || fs.NArg() > 0 {
		fmt.Fprint(stderr, coverageUsage)
		return 1
	}
	cutoff, err := parseSince(*since, time.Now())
	if err != nil {
		fmt.Fprintln(stderr, "afr:", err)
		return 1
	}
	dataDir, err := platform.DataDir()
	if err != nil {
		fmt.Fprintln(stderr, "afr:", err)
		return 1
	}
	results, used, err := checkCoverage(dataDir, *dir, cutoff)
	if err != nil {
		fmt.Fprintln(stderr, "afr:", err)
		return 1
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(results); err != nil {
			fmt.Fprintln(stderr, "afr:", err)
			return 1
		}
		return 0
	}
	var shown []coverage.Result
	for _, r := range results {
		if r.Prompts > 0 || !r.Logged { // skip sessions afr saw start where nothing was asked
			shown = append(shown, r)
		}
	}
	results = shown
	fmt.Fprintf(stdout, "transcripts in %s changed since %s\n\n", used, cutoff.Format("2006-01-02 15:04"))
	if len(results) == 0 {
		fmt.Fprintln(stdout, "no transcripts")
		return 0
	}
	coverageTable(stdout, results, time.Local)
	fmt.Fprintln(stdout, "\ncoverage is advisory: transcripts are not tamper-evident and Claude Code deletes them after about 30 days")
	return 0
}

// checkCoverage compares every transcript changed since the cutoff with
// afr's log of the same session. dir is the projects folder, or "" to find it.
func checkCoverage(dataDir, dir string, since time.Time) ([]coverage.Result, string, error) {
	rows, err := view.List(dataDir)
	if err != nil {
		return nil, "", err
	}
	if dir == "" {
		var known []string
		for _, r := range rows {
			known = append(known, transcriptPath(store.LogPath(dataDir, r.Session)))
		}
		var found bool
		dir, found = coverage.ProjectsDir(known)
		// The fallback is this machine's own transcripts: comparing them
		// with sessions from an export of another machine would list this
		// machine's conversations as missed.
		if !found {
			if h := otherHost(rows); h != "" {
				return nil, "", fmt.Errorf("the sessions were recorded on %s, not this machine; pass --transcripts with that machine's Claude Code projects folder", h)
			}
		}
	}
	paths, err := coverage.Transcripts(dir, since)
	if err != nil {
		return nil, dir, err
	}
	var out []coverage.Result
	for _, p := range paths {
		r, err := coverage.Read(p)
		if err != nil || r.Last.IsZero() {
			continue // unreadable, or nothing in it has a time
		}
		evs, _ := view.Load(store.LogPath(dataDir, r.Session))
		out = append(out, coverage.Compare(r, evs))
	}
	return out, dir, nil
}

// otherHost returns a host the sessions were recorded on that is not this
// machine, or "".
func otherHost(rows []view.Summary) string {
	local, err := os.Hostname()
	if err != nil {
		local = ""
	}
	for _, r := range rows {
		if r.Host != "" && !strings.EqualFold(r.Host, local) {
			return r.Host
		}
	}
	return ""
}

// transcriptPath reads the transcript a session's state points at.
func transcriptPath(logPath string) string {
	b, err := os.ReadFile(store.StatePath(logPath))
	if err != nil {
		return ""
	}
	var st struct {
		TranscriptPath string `json:"transcript_path"`
	}
	if json.Unmarshal(b, &st) != nil {
		return ""
	}
	return filepath.FromSlash(st.TranscriptPath)
}

func coverageTable(w io.Writer, results []coverage.Result, loc *time.Location) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "STARTED\tLAST\tSTATUS\tPROMPTS\tLIVE\tLATE\tMISSING\tAPP\tDIR\tSESSION")
	for _, r := range results {
		missing := fmt.Sprint(len(r.Missing))
		if n := len(r.Missing) - r.TypedMissing(); n > 0 {
			missing = fmt.Sprintf("%d (%d harness)", len(r.Missing), n)
		}
		status := r.Status
		if len(r.StopsNoAfr) > 0 {
			status += fmt.Sprintf(", %d turns without afr", len(r.StopsNoAfr))
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%d\t%s\t%s\t%s\t%s\n",
			r.First.In(loc).Format("2006-01-02 15:04"), r.Last.In(loc).Format("15:04:05"), status,
			r.Prompts, r.Hooked, r.Recovered, missing, appShort(r.App), shortDir(r.Cwd), cut8(r.Session))
		for _, g := range r.Missing {
			what := "typed prompt"
			if g.Harness {
				what = "harness message"
			}
			fmt.Fprintf(tw, "\t\t  not recorded: %s at %s\t\t\t\t\t\t\t\n", what, g.TS.In(loc).Format("2006-01-02 15:04:05"))
		}
	}
	tw.Flush()
}

func appShort(app string) string {
	return shortDir(strings.TrimPrefix(app, "claude-"))
}

// shortDir shortens a folder for the table, dropping control characters:
// the folder comes from a transcript, not from afr.
func shortDir(d string) string {
	d = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, d)
	if len([]rune(d)) <= 32 {
		return d
	}
	rs := []rune(d)
	return "..." + string(rs[len(rs)-29:])
}

func cut8(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}
