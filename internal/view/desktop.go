package view

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/obijones/afr/internal/schema"
)

// Messages the harness delivers through the prompt hook, and notes the
// Claude desktop app adds to the user's prompt (docs/findings.md F-008,
// F-009). They are recognized here so the replay, the session list, and
// the report label them the same way.

// subagentIDs returns the ids of every subagent seen in a session.
func subagentIDs(events []schema.Event) map[string]bool {
	ids := map[string]bool{}
	for _, e := range events {
		if e.AgentID != "" {
			ids[e.AgentID] = true
		}
	}
	return ids
}

// handbackAgent returns the subagent a prompt was delivered from: a
// subagent's report handed back to the session, or a background task
// notification. Only ids of subagents seen in the same session count,
// since a user can type the same text.
func handbackAgent(e schema.Event, agents map[string]bool) string {
	if e.EventType != schema.PromptSubmit || e.Content == nil {
		return ""
	}
	id, _ := parseHandback(e.Content.Text)
	if id != "" && agents[id] {
		return id
	}
	return ""
}

// parseHandback reads the subagent id from a hand-back message or task
// notification, and says which it is. The transcript's copy of a
// hand-back starts with a line naming its sender.
func parseHandback(text string) (id string, task bool) {
	text = strings.TrimSpace(text)
	text = strings.TrimSpace(strings.TrimPrefix(text, "Another Claude session sent a message:"))
	if rest, ok := strings.CutPrefix(text, `<agent-message from="`); ok {
		id, _, _ = strings.Cut(rest, `"`)
	} else if rest, ok := strings.CutPrefix(text, "<task-notification>"); ok {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(rest), "<task-id>"); ok {
			id, _, _ = strings.Cut(rest, "</task-id>")
			task = true
		}
	}
	return id, task
}

// taskSummary returns a task notification's status and summary.
func taskSummary(text string) string {
	tag := func(name string) string {
		_, rest, ok := strings.Cut(text, "<"+name+">")
		if !ok {
			return ""
		}
		v, _, _ := strings.Cut(rest, "</"+name+">")
		return strings.TrimSpace(v)
	}
	return strings.TrimSpace(tag("status") + ": " + tag("summary"))
}

// sessionCtx is what deciding a line's meaning needs from the whole
// session: its subagents, and when afr started recording it.
type sessionCtx struct {
	agents map[string]bool
	// joined is the time of the first event a hook recorded. Events
	// recovered from the transcript before it happened while afr's hooks
	// were not active yet: the session was recorded late (F-011).
	joined time.Time
}

func newSessionCtx(events []schema.Event) sessionCtx {
	sc := sessionCtx{agents: subagentIDs(events)}
	for _, e := range events {
		if e.Origin == "" && (sc.joined.IsZero() || e.TS.Before(sc.joined)) {
			sc.joined = e.TS
		}
	}
	return sc
}

// late reports an event recovered from the transcript from before afr
// started recording the session.
func (sc sessionCtx) late(e schema.Event) bool {
	return e.Origin == schema.OriginTranscript && !sc.joined.IsZero() && e.TS.Before(sc.joined)
}

// joinedLate reports whether any events predate afr's first hook.
func (sc sessionCtx) joinedLate(events []schema.Event) bool {
	for _, e := range events {
		if sc.late(e) {
			return true
		}
	}
	return false
}

// harnessText reports prompt text the harness writes itself: slash
// command echoes, skill content, image placeholders, notices.
func harnessText(text string) bool {
	t := strings.TrimSpace(text)
	for _, p := range []string{"<command-name>", "<command-message>", "<command-args>", "<local-command-", "<task-notification>",
		"<agent-message", "Another Claude session sent a message:", "[Image", "Base directory for this skill:", "Caveat:"} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// isUserPrompt reports a prompt a person sent: through the prompt hook,
// or from before afr joined a session when it is not harness text; and
// never a subagent's report or task notification the harness delivered.
func (sc sessionCtx) isUserPrompt(e schema.Event) bool {
	if e.EventType != schema.PromptSubmit || handbackAgent(e, sc.agents) != "" {
		return false
	}
	if e.Origin == "" {
		return true
	}
	if !sc.late(e) || e.Meta || e.Content == nil {
		return false
	}
	_, typed := splitAppNotes(e.Content.Text)
	return strings.TrimSpace(typed) != "" && !harnessText(typed)
}

// inferApp recognizes a desktop session in a log without an entrypoint:
// only the desktop app works in its scratch-workspaces folder.
func inferApp(events []schema.Event) string {
	for _, e := range events {
		if strings.Contains(strings.ToLower(strings.ReplaceAll(e.Cwd, `\`, "/")), "/claude/scratch-workspaces/") {
			return "desktop"
		}
	}
	return ""
}

// appName shortens a transcript entrypoint for display.
func appName(entrypoint string) string {
	switch entrypoint {
	case "claude-desktop":
		return "desktop"
	}
	return entrypoint
}

// handbackReport returns the report inside a hand-back message, without
// the harness's frame and indentation, or the text as is.
func handbackReport(text string) string {
	const marker = "The report follows:"
	i := strings.Index(text, marker)
	if i < 0 {
		return text
	}
	body := strings.TrimSuffix(strings.TrimSpace(text[i+len(marker):]), "</agent-message>")
	lines := strings.Split(body, "\n")
	for j, l := range lines {
		lines[j] = strings.TrimPrefix(l, "  ")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// harnessWarning splits the notice Claude Code puts before a subagent's
// report when the report matches its instruction-shaped patterns:
// "[harness: subagent output matched instruction-shaped pattern(s): P. ...]".
// It returns the patterns, the whole notice, and the report without it.
func harnessWarning(report string) (patterns, warning, rest string) {
	const prefix = "[harness: subagent output matched instruction-shaped pattern(s):"
	if !strings.HasPrefix(report, prefix) {
		return "", "", report
	}
	end := strings.Index(report, "]")
	if end < 0 {
		return "", "", report
	}
	warning = report[1:end]
	patterns, _, _ = strings.Cut(strings.TrimSpace(report[len(prefix):end]), ".")
	return strings.TrimSpace(patterns), warning, strings.TrimSpace(report[end+1:])
}

// splitAppNotes separates <system-reminder> blocks at the start of a
// prompt, which the desktop app adds, from what the user typed.
func splitAppNotes(text string) (notes, user string) {
	const open, end = "<system-reminder>", "</system-reminder>"
	rest := strings.TrimLeft(text, " \t\r\n")
	var got []string
	for strings.HasPrefix(rest, open) {
		j := strings.Index(rest, end)
		if j < 0 {
			break
		}
		got = append(got, strings.TrimSpace(rest[len(open):j]))
		rest = strings.TrimLeft(rest[j+len(end):], " \t\r\n")
	}
	if len(got) == 0 {
		return "", text
	}
	return strings.Join(got, "\n\n"), rest
}

// handbackMessages maps each subagent to the report it handed back with
// the SubagentHandback tool, for subagents whose stop event has no text.
func handbackMessages(events []schema.Event) map[string]string {
	out := map[string]string{}
	for _, e := range events {
		if e.EventType != schema.ToolCall || e.Tool != "SubagentHandback" || e.AgentID == "" || e.Content == nil {
			continue
		}
		var in struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(e.Content.Input, &in) == nil && in.Message != "" {
			out[e.AgentID] = in.Message
		}
	}
	return out
}
