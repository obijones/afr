// Package coverage compares Claude Code transcripts with afr's logs to find
// what afr did not record: conversations it never saw and prompts that ran
// while its hooks were missing (docs/findings.md F-014).
//
// Coverage is advisory. Transcripts are not hash-chained, the user who can
// remove afr's hooks can also edit or delete them, and Claude Code removes
// them after about 30 days, so a missing transcript says nothing.
package coverage

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/obijones/afr/internal/schema"
	"github.com/obijones/afr/internal/transcript"
)

// Statuses.
const (
	Complete = "complete" // every prompt reached afr through its hooks
	Late     = "late"     // every prompt is in afr's log, some only recovered from the transcript afterwards
	Partial  = "partial"  // afr's log lacks typed prompts the transcript has
	Missed   = "missed"   // afr has no log of the session at all
)

// Result is what one transcript says about afr's record of its session.
// It holds only times, counts, and folders, never conversation text.
type Result struct {
	Session    string      `json:"session"`
	App        string      `json:"app,omitempty"` // claude-desktop, cli, ...
	Cwd        string      `json:"cwd,omitempty"` // the last folder the transcript names
	First      time.Time   `json:"first"`
	Last       time.Time   `json:"last"`
	Logged     bool        `json:"logged"` // afr has a log of this session
	Status     string      `json:"status"`
	Prompts    int         `json:"prompts"`   // prompts in the transcript, harness messages included
	Harness    int         `json:"harness"`   // of those, messages the harness delivered (subagent reports, task notices)
	Hooked     int         `json:"hooked"`    // recorded live by afr's prompt hook
	Recovered  int         `json:"recovered"` // recorded later from the transcript
	Missing    []Gap       `json:"missing,omitempty"`
	Stops      int         `json:"stops"`                       // turn ends where Claude Code listed the Stop hooks that ran
	StopsNoAfr []time.Time `json:"stops_without_afr,omitempty"` // turn ends where hooks ran but afr was not among them

	prompts []prompt // every counted prompt, for Compare
}

type prompt struct {
	id  string
	gap Gap
}

// Gap is one prompt afr has no record of.
type Gap struct {
	TS      time.Time `json:"ts"`
	Harness bool      `json:"harness,omitempty"` // delivered by the harness, not typed
}

// afrHook matches the command of an afr hook as Claude Code lists it:
// "C:\Users\u\afr\afr.exe hook", "/usr/local/bin/afr hook", quoted or not.
var afrHook = regexp.MustCompile(`(?i)(^|[\\/"'\s])afr(\.exe)?["']?\s+hook\b`)

// IsAfrHook reports whether a hook command runs afr.
func IsAfrHook(command string) bool { return afrHook.MatchString(command) }

// Read collects what a transcript says about its prompts and turn ends.
func Read(path string) (Result, error) {
	r := Result{Session: strings.TrimSuffix(filepath.Base(path), ".jsonl")}
	recs, err := readAll(path)
	if err != nil {
		return r, err
	}
	seen := map[string]bool{}
	for _, rec := range recs {
		if !rec.TS.IsZero() {
			if r.First.IsZero() || rec.TS.Before(r.First) {
				r.First = rec.TS
			}
			if rec.TS.After(r.Last) {
				r.Last = rec.TS
			}
		}
		if rec.Cwd != "" {
			r.Cwd = rec.Cwd
		}
		if rec.App != "" {
			r.App = rec.App
		}
		switch rec.Kind {
		case transcript.HookSummary:
			r.Stops++
			if !slices.ContainsFunc(rec.Hooks, IsAfrHook) {
				r.StopsNoAfr = append(r.StopsNoAfr, rec.TS)
			}
		case transcript.Prompt:
			// A message the harness writes itself (isMeta) never goes
			// through the prompt hook, so it is not counted.
			// Nor are compaction summaries and slash commands Claude Code
			// ran itself: none of them is a prompt to the model.
			if rec.Meta || rec.Summary || rec.PromptID == "" || seen[rec.PromptID] || localCommand(rec.Text) {
				continue
			}
			seen[rec.PromptID] = true
			r.Prompts++
			h := harnessDelivery(rec.Text)
			if h {
				r.Harness++
			}
			r.prompts = append(r.prompts, prompt{rec.PromptID, Gap{TS: rec.TS, Harness: h}})
		}
	}
	return r, nil
}

// Compare fills in a result from afr's events for the same session; nil
// events mean afr has no log of it.
func Compare(r Result, events []schema.Event) Result {
	hooked, recovered := map[string]bool{}, map[string]bool{}
	for _, e := range events {
		if e.EventType != schema.PromptSubmit || e.TurnID == "" {
			continue
		}
		if e.Origin == schema.OriginTranscript {
			recovered[e.TurnID] = true
		} else {
			hooked[e.TurnID] = true
		}
	}
	r.Logged = len(events) > 0
	r.Hooked, r.Recovered, r.Missing = 0, 0, nil
	typedLate := 0 // typed prompts afr only has from the transcript
	for _, p := range r.prompts {
		switch {
		case hooked[p.id]:
			r.Hooked++
		case recovered[p.id]:
			r.Recovered++
			if !p.gap.Harness {
				typedLate++
			}
		default:
			r.Missing = append(r.Missing, p.gap)
		}
	}
	switch {
	case !r.Logged:
		r.Status = Missed
	case r.TypedMissing() > 0:
		r.Status = Partial
	// A harness message (a task notice) can arrive after afr's last hook
	// of a turn and wait in the transcript until afr next runs, so on its
	// own it is "not yet recovered", not a gap.
	case typedLate > 0 || len(r.StopsNoAfr) > 0:
		// afr was not running live for part of the session (F-011).
		// Harness messages never pass through the prompt hook, so afr
		// recovering them is normal and does not count.
		r.Status = Late
	default:
		r.Status = Complete
	}
	return r
}

// TypedMissing counts the missing prompts a person typed.
func (r Result) TypedMissing() int {
	n := 0
	for _, g := range r.Missing {
		if !g.Harness {
			n++
		}
	}
	return n
}

// localCommand reports whether a transcript message records a slash
// command Claude Code ran itself (/model, /exit): it never reaches the
// model or the prompt hook.
func localCommand(text string) bool {
	t := strings.TrimSpace(text)
	for _, p := range []string{"<command-name>", "<command-message>", "<local-command-stdout>", "<local-command-stderr>", "<local-command-caveat>"} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// harnessDelivery reports whether a prompt is a message the harness
// delivered: a subagent's report or a background task notice (F-009).
func harnessDelivery(text string) bool {
	t := strings.TrimSpace(text)
	t = strings.TrimSpace(strings.TrimPrefix(t, "Another Claude session sent a message:"))
	return strings.HasPrefix(t, "<agent-message") || strings.HasPrefix(t, "<task-notification>")
}

func readAll(path string) ([]transcript.Record, error) {
	var all []transcript.Record
	var off int64
	for {
		recs, next, err := transcript.ReadFrom(path, off, 4<<20)
		if err != nil {
			return all, err
		}
		all = append(all, recs...)
		if next == off {
			return all, nil
		}
		off = next
	}
}

// Transcripts lists the main transcripts under a Claude Code projects
// folder (<dir>/<project>/<session>.jsonl, subagent files excluded) that
// changed since the cutoff, oldest first.
func Transcripts(dir string, since time.Time) ([]string, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*", "*.jsonl"))
	if err != nil {
		return nil, err
	}
	type file struct {
		path string
		mod  time.Time
	}
	var out []file
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil || !info.Mode().IsRegular() || (!since.IsZero() && info.ModTime().Before(since)) {
			continue
		}
		out = append(out, file{p, info.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].mod.Before(out[j].mod) })
	list := make([]string, len(out))
	for i, f := range out {
		list[i] = f.path
	}
	return list, nil
}

// ProjectsDir finds the Claude Code projects folder: the one afr's own
// session state points at (transcript_path is <projects>/<project>/<id>.jsonl),
// else ~/.claude/projects.
// found is false for the fallback, which is right only for sessions
// recorded on this machine.
func ProjectsDir(transcriptPaths []string) (dir string, found bool) {
	for _, p := range transcriptPaths {
		// A path from another OS (C:\... read on Linux) is not absolute
		// here, and its parent would come out as ".".
		if !filepath.IsAbs(p) {
			continue
		}
		dir := filepath.Dir(filepath.Dir(p))
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir, true
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(home, ".claude", "projects"), false
}
