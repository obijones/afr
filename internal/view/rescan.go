package view

import (
	"os"
	"slices"
	"strings"

	"github.com/obijones/afr/internal/rules"
	"github.com/obijones/afr/internal/schema"
)

// Rescan runs today's highlight rules over a recorded session and returns,
// by event id, the highlights the log does not already have: a rule that
// did not exist when the session was recorded, or a severity that changed.
// It never changes the log; stored highlights stay the record.
//
// Events already evaluated by this rules version are skipped, as are
// events whose content was purged (they cannot be re-evaluated).
func Rescan(events []schema.Event, dataDir string) map[string][]schema.Highlight {
	ctx := rules.Context{DataDir: dataDir}
	for _, e := range InTimeOrder(events) {
		if e.Cwd != "" {
			ctx.Workspace = e.Cwd
			break
		}
	}
	ctx.Home = homeFor(ctx.Workspace)

	out := map[string][]schema.Highlight{}
	for _, e := range InTimeOrder(events) {
		if !rules.Evaluates(e) || e.EventID == "" {
			continue
		}
		if e.RulesVersion == rules.Version || (e.Content == nil && e.ContentHash != "") {
			if e.PermissionMode != "" {
				ctx.Mode = e.PermissionMode
			}
			continue
		}
		current, err := rules.Evaluate(e, &ctx)
		if err != nil && current == nil {
			continue
		}
		var add []schema.Highlight
		for _, h := range current {
			if !slices.ContainsFunc(e.Highlights, func(s schema.Highlight) bool {
				return s.RuleID == h.RuleID && s.Severity == h.Severity
			}) {
				add = append(add, h)
			}
		}
		if len(add) > 0 {
			out[e.EventID] = add
		}
	}
	return out
}

// homeFor guesses the recording user's home folder from the workspace
// path (/home/<u>, /Users/<u>, C:\Users\<u>), since the log may come
// from another machine. Otherwise it uses the reviewer's own home.
func homeFor(workspace string) string {
	p := strings.ReplaceAll(workspace, `\`, "/")
	parts := strings.Split(p, "/")
	for i := 0; i+1 < len(parts); i++ {
		switch strings.ToLower(parts[i]) {
		case "home", "users":
			if i <= 1 && parts[i+1] != "" {
				return strings.Join(parts[:i+2], "/")
			}
		}
	}
	h, _ := os.UserHomeDir()
	return h
}

func countHighlights(m map[string][]schema.Highlight) int {
	n := 0
	for _, hs := range m {
		n += len(hs)
	}
	return n
}

// rankHighlights orders highlights by severity, highest first, dropping
// exact repeats (a secret seen in both a call and its result).
func rankHighlights(hs []schema.Highlight) []schema.Highlight {
	var out []schema.Highlight
	for _, h := range hs {
		if !slices.ContainsFunc(out, func(o schema.Highlight) bool { return o.RuleID == h.RuleID && o.Severity == h.Severity }) {
			out = append(out, h)
		}
	}
	slices.SortStableFunc(out, func(a, b schema.Highlight) int { return rules.Rank(b.Severity) - rules.Rank(a.Severity) })
	return out
}
