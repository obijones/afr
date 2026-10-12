package view

import (
	"encoding/json"
	"strings"

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

// isUserPrompt reports a prompt a person sent: through the prompt hook,
// and not a subagent's report or task notification the harness delivered.
func isUserPrompt(e schema.Event, agents map[string]bool) bool {
	return e.EventType == schema.PromptSubmit && e.Origin == "" && handbackAgent(e, agents) == ""
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
