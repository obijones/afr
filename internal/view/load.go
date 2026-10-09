// Package view builds what a reviewer reads from session logs: the
// session list and the replay of one session.
package view

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/obijones/afr/internal/schema"
	"github.com/obijones/afr/internal/store"
)

// Load reads a session log in write order. Unparseable lines are skipped
// here; afr verify reports them.
func Load(path string) ([]schema.Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []schema.Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var e schema.Event
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}

// InTimeOrder returns the events sorted by timestamp. Events recovered
// from the transcript are written after the hooks that triggered the
// read, so write order is not the order things happened. Ties keep write
// order.
func InTimeOrder(events []schema.Event) []schema.Event {
	out := slices.Clone(events)
	sort.SliceStable(out, func(i, j int) bool { return out[i].TS.Before(out[j].TS) })
	return out
}

// Resolve finds a session log by full id or unique id prefix.
func Resolve(dataDir, id string) (string, error) {
	if id == "" {
		return "", errors.New("no session id given")
	}
	exact := store.LogPath(dataDir, id)
	if _, err := os.Stat(exact); err == nil {
		return exact, nil
	}
	matches, _ := filepath.Glob(filepath.Join(store.SessionsDir(dataDir), "*.ndjson"))
	var hits []string
	for _, m := range matches {
		if strings.HasPrefix(filepath.Base(m), id) {
			hits = append(hits, m)
		}
	}
	switch len(hits) {
	case 0:
		return "", fmt.Errorf("no session matches %q", id)
	case 1:
		return hits[0], nil
	default:
		return "", fmt.Errorf("%q matches %d sessions; use more of the id", id, len(hits))
	}
}

// Summary is one row of the session list.
type Summary struct {
	Session   string
	Started   time.Time
	LastSeen  time.Time
	Status    string // "ended (reason)" or "open"
	Runs      int    // session starts, including resumes
	Turns     int
	ToolCalls int
	Highlight int
	User      string
	Host      string
	Cwd       string
	Agent     string
}

// Summarize computes a session's summary from its events.
func Summarize(events []schema.Event) Summary {
	var s Summary
	open := false
	for _, e := range InTimeOrder(events) {
		if s.Session == "" {
			s.Session, s.Started = e.Session, e.TS
		}
		s.LastSeen = e.TS
		if e.User != "" {
			s.User, s.Host = e.User, e.Host
		}
		if e.Cwd != "" && s.Cwd == "" {
			s.Cwd = e.Cwd
		}
		if e.Agent != "" {
			s.Agent = e.Agent
		}
		s.Highlight += len(e.Highlights)
		switch e.EventType {
		case schema.SessionStart:
			s.Runs++
			open = true
		case schema.SessionEnd:
			open = false
			s.Status = "ended"
			if e.EndReason != "" {
				s.Status += " (" + e.EndReason + ")"
			}
		case schema.PromptSubmit:
			s.Turns++
		case schema.ToolCall:
			s.ToolCalls++
		}
	}
	if open || s.Status == "" {
		s.Status = "open"
	}
	return s
}

// List summarizes every session under dataDir, most recent activity first.
func List(dataDir string) ([]Summary, error) {
	paths, err := filepath.Glob(filepath.Join(store.SessionsDir(dataDir), "*.ndjson"))
	if err != nil {
		return nil, err
	}
	var out []Summary
	for _, p := range paths {
		evs, err := Load(p)
		if err != nil || len(evs) == 0 {
			continue
		}
		out = append(out, Summarize(evs))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen.After(out[j].LastSeen) })
	return out, nil
}
