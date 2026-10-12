package view

import (
	"fmt"
	"strings"

	"github.com/obijones/afr/internal/schema"
)

// Model is one session as a reviewer reads it: a summary and one line per
// thing that happened, in time order, each tool call paired with its
// result. The terminal replay and the HTML report both render it, so they
// cannot disagree.
type Model struct {
	Summary Summary
	Lines   []Line
	// CurrentCount is how many highlights come from today's rules rather
	// than the log (see Rescan).
	CurrentCount int
}

// Line is one row of a replay. Text holds captured data as is: renderers
// cut it to fit and neutralize control characters for their medium.
type Line struct {
	Event   schema.Event  // the event shown; a tool call for TOOL lines
	Result  *schema.Event // TOOL lines: the call's result, when one was logged
	Kind    string        // START, PROMPT, AI, TOOL, RESULT, HELPER, SUBAGENT, TURN END, END, NOTICE, NOTE
	Turn    string        // t1, t2, ... for events in a turn, else empty
	Marks   []Mark        // highlights, highest severity first
	Note    string        // a label such as "injected by the harness" or "accepted suggestion"
	Text    string        // the line after its prefix, on one line
	Tool    *ToolLine     // TOOL lines: the parts of Text, so a renderer can fit them
	Details []Detail      // full values shown under the line on request
}

// ToolLine is a tool call's line in parts.
type ToolLine struct {
	Name    string `json:"name"`             // tool name, with "(no hook ran)" when the harness ran it alone
	Summary string `json:"summary"`          // the command, path, or other input a reviewer needs
	Status  string `json:"status"`           // result in a few words
	Failed  bool   `json:"failed,omitempty"` // the call failed or was refused
	Denied  bool   `json:"denied,omitempty"` // auto mode refused it
}

// Detail is one full value: a prompt, a reply, tool input or output.
type Detail struct {
	Label string
	Body  string
}

// Mark is a highlight as a line shows it.
type Mark struct {
	schema.Highlight
	Current bool // from today's rules, not stored in the log
}

// Label is the rule id, marked * when the highlight comes from today's
// rules.
func (m Mark) Label() string {
	if m.Current {
		return m.RuleID + "*"
	}
	return m.RuleID
}

// Build turns a session's events into its replay model. current holds
// highlights today's rules add, by event id (nil for none).
func Build(events []schema.Event, current map[string][]schema.Highlight) (*Model, error) {
	if len(events) == 0 {
		return nil, fmt.Errorf("session log is empty")
	}
	evs := InTimeOrder(events)
	b := &builder{current: current, turns: map[string]string{}, results: map[string]schema.Event{}, accepted: map[string]bool{},
		sc: newSessionCtx(events), handbacks: handbackMessages(events), agentTypes: map[string]string{}}
	b.agents = b.sc.agents
	for _, e := range evs {
		if e.AgentID != "" && e.AgentType != "" {
			b.agentTypes[e.AgentID] = e.AgentType
		}
	}
	// A hand-back the prompt hook recorded also comes back from the
	// transcript; the hook's copy is shown once.
	hooked := map[string]bool{}
	for _, e := range evs {
		if id := handbackAgent(e, b.agents); id != "" && e.Origin == "" {
			if _, task := parseHandback(e.Content.Text); !task {
				hooked[id] = true
			}
		}
	}
	m := &Model{Summary: Summarize(events), CurrentCount: countHighlights(current)}

	// A subagent end with no agent type is one of Claude Code's own
	// background tasks (prompt suggestions, session recap), not work the
	// model delegated. A prompt equal to the suggestion just before it was
	// most likely accepted from that suggestion.
	var suggestion *schema.Event
	for i := range evs {
		e := &evs[i]
		switch {
		case isHelper(*e):
			suggestion = e
		case e.EventType == schema.PromptSubmit && e.Origin == "":
			if suggestion != nil && e.Content != nil && suggestion.Content != nil &&
				strings.TrimSpace(e.Content.Text) == strings.TrimSpace(suggestion.Content.Text) && e.Content.Text != "" {
				b.accepted[e.EventID+e.TS.String()] = true
				b.accepted[suggestion.EventID+suggestion.TS.String()] = true
			}
			suggestion = nil
		}
	}

	// Turns are numbered by the prompts people sent. A turn started by
	// something the harness delivered (a subagent's report, a notice)
	// carries the number of the user turn before it, or none.
	calls := map[string]bool{}
	n, last := 0, ""
	for _, e := range evs {
		if e.EventType != schema.PromptSubmit || e.TurnID == "" {
			continue
		}
		if b.sc.isUserPrompt(e) {
			if _, ok := b.userTurns[e.TurnID]; !ok {
				n++
				if b.userTurns == nil {
					b.userTurns = map[string]bool{}
				}
				b.userTurns[e.TurnID] = true
				b.turns[e.TurnID] = fmt.Sprintf("t%d", n)
			}
			last = b.turns[e.TurnID]
		} else if _, ok := b.turns[e.TurnID]; !ok && last != "" {
			b.turns[e.TurnID] = last
		}
	}
	for _, e := range evs {
		if e.EventType == schema.ToolResult && e.ToolCallID != "" {
			b.results[e.ToolCallID] = e
		}
		if e.EventType == schema.ToolCall {
			calls[e.ToolCallID] = true
		}
	}
	noted := !b.sc.joinedLate(evs)
	for _, e := range evs {
		if e.EventType == schema.ToolResult && calls[e.ToolCallID] {
			continue // shown with its call
		}
		if !noted && !b.sc.late(e) {
			// Where afr's hooks started recording a session already under way.
			noted = true
			m.Lines = append(m.Lines, Line{
				Event: schema.Event{TS: b.sc.joined},
				Kind:  "NOTE",
				Text:  "afr started recording here; the lines above were recovered from the transcript",
			})
		}
		if id := handbackAgent(e, b.agents); id != "" && e.Origin == schema.OriginTranscript && hooked[id] {
			if _, task := parseHandback(e.Content.Text); !task {
				continue // the transcript's copy of a hand-back the hook recorded
			}
		}
		m.Lines = append(m.Lines, b.line(e))
	}
	return m, nil
}

type builder struct {
	sc         sessionCtx
	agents     map[string]bool   // subagent ids seen in the session
	handbacks  map[string]string // subagent id -> report handed back with SubagentHandback
	agentTypes map[string]string // subagent id -> type
	userTurns  map[string]bool   // turn ids started by a person's prompt
	current    map[string][]schema.Highlight
	turns      map[string]string       // turn_id -> t1, t2, ...
	results    map[string]schema.Event // tool_call_id -> result
	accepted   map[string]bool         // prompts sent from a suggestion, and those suggestions, by event id and time
}

// marks merges what a line shows: the event's stored highlights, a tool
// call's result highlights, and current-rule additions, highest severity
// first, one per rule and severity. A current-rule highlight is kept apart
// from a stored one with the same rule.
func (b *builder) marks(e schema.Event) []Mark {
	stored := append([]schema.Highlight(nil), e.Highlights...)
	extra := append([]schema.Highlight(nil), b.current[e.EventID]...)
	if e.EventType == schema.ToolCall {
		if res, ok := b.results[e.ToolCallID]; ok {
			stored = append(stored, res.Highlights...)
			extra = append(extra, b.current[res.EventID]...)
		}
	}
	for i := range extra {
		extra[i].RuleID += "*" // rule ids never contain *, so this only separates them while ranking
	}
	ranked := rankHighlights(append(stored, extra...))
	if len(ranked) == 0 {
		return nil
	}
	out := make([]Mark, len(ranked))
	for i, h := range ranked {
		cur := strings.HasSuffix(h.RuleID, "*")
		h.RuleID = strings.TrimSuffix(h.RuleID, "*")
		out[i] = Mark{Highlight: h, Current: cur}
	}
	return out
}

func (b *builder) line(e schema.Event) Line {
	c := e.Content
	if c == nil {
		c = &schema.Content{}
	}
	l := Line{Event: e, Turn: b.turns[e.TurnID], Marks: b.marks(e)}
	if e.EventType == schema.SessionStart || e.EventType == schema.SessionEnd {
		l.Turn = "" // the session's, not a turn's
	}
	detail := func(label, body string) { l.Details = append(l.Details, Detail{label, body}) }
	switch e.EventType {
	case schema.SessionStart:
		src := e.Source
		if src == "" {
			src = "start"
		}
		l.Kind, l.Text = "START", src
	case schema.PromptSubmit:
		if id := handbackAgent(e, b.agents); id != "" {
			// A subagent's report or task notice, delivered by the harness
			// through the prompt hook (docs/findings.md F-009).
			from := strings.TrimSpace(b.agentTypes[id] + " " + cut(id, 8))
			l.Kind, l.Note = "HANDBACK", "delivered by the harness"
			if _, task := parseHandback(c.Text); task {
				l.Text = "task notice for " + from + ": " + oneLine(taskSummary(c.Text))
			} else {
				l.Text = "report from " + from + ": " + quote(oneLine(handbackReport(c.Text)))
			}
			detail("message", c.Text)
			break
		}
		notes, typed := splitAppNotes(c.Text)
		var tags []string
		if e.Origin == schema.OriginTranscript && !b.sc.isUserPrompt(e) {
			tags = append(tags, "injected by the harness")
		} else if b.accepted[e.EventID+e.TS.String()] {
			tags = append(tags, "accepted suggestion")
		}
		shown := typed
		switch {
		case notes != "" && strings.TrimSpace(typed) == "":
			tags = append(tags, "note from the app") // nothing typed: the message is the note
			shown, typed = notes, ""
		case notes != "":
			tags = append(tags, "app note added") // the desktop app's text before the user's (F-008)
		}
		l.Note = strings.Join(tags, ", ")
		l.Kind, l.Text = "PROMPT", quote(oneLine(shown))
		if l.Note != "" {
			l.Text = "(" + l.Note + ") " + l.Text
		}
		detail("prompt", typed)
		detail("added by the app", notes)
	case schema.AssistantMessage:
		l.Kind, l.Text = "AI", quote(oneLine(c.Text))
		detail("reply", c.Text)
	case schema.ToolCall:
		res, ok := b.results[e.ToolCallID]
		t := &ToolLine{Name: e.Tool, Status: "no result (refused, interrupted, or failed)"}
		if ok {
			l.Result = &res
			t.Status = resultSummary(e.Tool, res)
			t.Failed = res.ToolError
			t.Denied = res.ToolError && res.HookEvent == "PermissionDenied"
		}
		if e.Origin == schema.OriginTranscript && !b.sc.late(e) {
			t.Name += " (no hook ran)" // handled by the harness itself; not said of calls from before afr joined
		}
		t.Summary, t.Status = oneLine(relativeTo(e.Cwd, toolSummary(e.Tool, c.Input))), oneLine(t.Status)
		l.Kind, l.Tool = "TOOL", t
		l.Text = t.Name + "  " + t.Summary + "  ->  " + t.Status
		detail("input", pretty(c.Input))
		if ok && res.Content != nil {
			detail("output", outputText(res.Content.Output))
		}
	case schema.ToolResult: // a result whose call was not logged
		l.Kind, l.Text = "RESULT", fmt.Sprintf("%-5s  ->  %s", e.Tool, oneLine(resultSummary(e.Tool, e)))
		detail("output", outputText(c.Output))
	case schema.SubagentEnd:
		if isHelper(e) {
			what := "Claude Code background task: "
			if b.accepted[e.EventID+e.TS.String()] {
				what = "Claude Code suggested the next prompt: "
			}
			if strings.TrimSpace(c.Text) == "" {
				l.Kind, l.Text = "HELPER", "Claude Code background task (no output)"
				break
			}
			l.Kind, l.Text = "HELPER", what+quote(oneLine(c.Text))
			detail("output", c.Text)
			break
		}
		report := c.Text
		if strings.TrimSpace(report) == "" {
			report = b.handbacks[e.AgentID] // background subagents report through SubagentHandback
		}
		l.Kind, l.Text = "SUBAGENT", "finished: "+quote(oneLine(report))
		detail("report", report)
	case schema.TurnEnd:
		l.Kind = "TURN END"
	case schema.SessionEnd:
		reason := e.EndReason
		if reason == "" {
			reason = "ended"
		}
		l.Kind, l.Text = "END", reason
	case schema.HarnessNotice:
		l.Kind, l.Text = "NOTICE", noticeSummary(e)
		detail("notice", c.Text)
		if e.Notice != nil {
			detail("explanation", e.Notice.Explanation)
		}
	case schema.AfrError:
		l.Kind, l.Text = "NOTE", "afr: "+oneLine(c.Text)
	default:
		l.Kind, l.Text = "NOTE", e.HookEvent
	}
	return l
}
