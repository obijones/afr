package view

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/obijones/afr/internal/schema"
)

// Options controls how a replay is printed.
type Options struct {
	Full  bool           // print complete text, inputs, and outputs under each line
	Width int            // cut summary lines to this many characters (0 = 110)
	Loc   *time.Location // time zone for timestamps (nil = local)
}

// Replay prints one session as a conversation, start to end, in the
// order things happened. Each tool call is shown with its result.
// integrity is a one-line verdict from the verifier, shown in the header.
func Replay(w io.Writer, events []schema.Event, integrity string, o Options) error {
	if len(events) == 0 {
		return fmt.Errorf("session log is empty")
	}
	if o.Width == 0 {
		o.Width = 110
	}
	if o.Loc == nil {
		o.Loc = time.Local
	}
	evs := InTimeOrder(events)
	sum := Summarize(events)
	r := &replayer{w: w, o: o, turns: map[string]string{}, results: map[string]schema.Event{}}

	for _, e := range evs {
		if e.EventType == schema.PromptSubmit && e.TurnID != "" {
			if _, ok := r.turns[e.TurnID]; !ok {
				r.turns[e.TurnID] = fmt.Sprintf("t%d", len(r.turns)+1)
			}
		}
		if e.EventType == schema.ToolResult && e.ToolCallID != "" {
			r.results[e.ToolCallID] = e
		}
	}

	start := evs[0].TS.In(o.Loc)
	fmt.Fprintf(w, "SESSION %s  %s@%s  %s  %s\n", sum.Session, sum.User, sum.Host, sum.Cwd, sum.Agent)
	fmt.Fprintf(w, "%s  %s, %s, %s  integrity: %s\n\n", start.Format("2006-01-02 MST"),
		plural(sum.Turns, "turn"), plural(sum.ToolCalls, "tool call"), plural(sum.Highlight, "highlight"), integrity)

	calls := map[string]bool{}
	for _, e := range evs {
		if e.EventType == schema.ToolCall {
			calls[e.ToolCallID] = true
		}
	}
	for _, e := range evs {
		if e.EventType == schema.ToolResult && calls[e.ToolCallID] {
			continue // printed with its call
		}
		r.event(e)
	}
	return r.err
}

type replayer struct {
	w       io.Writer
	o       Options
	turns   map[string]string       // turn_id -> t1, t2, ...
	results map[string]schema.Event // tool_call_id -> result
	err     error
}

// prefixFor builds the start of a line: time, kind, turn, then any
// highlights and the subagent tag. Built without a format string, since
// rule ids and agent types come from captured data.
func (r *replayer) prefixFor(e schema.Event, kind string) string {
	turn := r.turns[e.TurnID]
	if turn == "" {
		turn = "-"
	}
	p := fmt.Sprintf("%s  %-9s %-3s ", e.TS.In(r.o.Loc).Format("15:04:05"), kind, turn)
	// Highlights go first, so a long line can never cut them off.
	for _, h := range e.Highlights {
		p += "[" + strings.ToUpper(h.Severity) + " " + h.RuleID + "] "
	}
	if e.AgentType != "" {
		p += "[" + e.AgentType + "] "
	}
	return p
}

func (r *replayer) line(e schema.Event, kind, text string) {
	s := neutralize(r.prefixFor(e, kind)+text, false)
	if len(s) > r.o.Width {
		s = cut(s, r.o.Width-3) + "..."
	}
	r.write(s + "\n")
}

// detail prints a full value under its line, indented, in --full mode.
func (r *replayer) detail(label, body string) {
	if !r.o.Full || strings.TrimSpace(body) == "" {
		return
	}
	r.write("          " + label + ":\n")
	for _, l := range strings.Split(strings.TrimRight(neutralize(body, true), "\n"), "\n") {
		r.write("            " + l + "\n")
	}
}

func (r *replayer) write(s string) {
	if r.err == nil {
		_, r.err = io.WriteString(r.w, s)
	}
}

func (r *replayer) event(e schema.Event) {
	c := e.Content
	if c == nil {
		c = &schema.Content{}
	}
	switch e.EventType {
	case schema.SessionStart:
		src := e.Source
		if src == "" {
			src = "start"
		}
		r.line(e, "START", src)
	case schema.PromptSubmit:
		note := ""
		if e.Origin == schema.OriginTranscript {
			note = "(injected by the harness) "
		}
		r.line(e, "PROMPT", note+quote(oneLine(c.Text)))
		r.detail("prompt", c.Text)
	case schema.AssistantMessage:
		r.line(e, "AI", quote(oneLine(c.Text)))
		r.detail("reply", c.Text)
	case schema.ToolCall:
		res, ok := r.results[e.ToolCallID]
		status := "no result (refused, interrupted, or failed)"
		if ok {
			status = resultSummary(e.Tool, res)
		}
		name := e.Tool
		if e.Origin == schema.OriginTranscript {
			name += " (no hook ran)" // handled by the harness itself
		}
		// Fit both command and status: when the line is too long, the
		// status gets at most about 40% of the room, the command the rest.
		head := fmt.Sprintf("%-5s  ", name)
		const sep = "  ->  "
		summary, st := oneLine(toolSummary(e.Tool, c.Input)), oneLine(status)
		avail := r.o.Width - len(neutralize(r.prefixFor(e, "TOOL"), false)) - len(head) - len(sep)
		if len(summary)+len(st) > avail {
			if maxSt := max(avail*2/5, 24); len(st) > maxSt {
				st = cut(st, maxSt-3) + "..."
			}
			if room := max(avail-len(st), 20); len(summary) > room {
				summary = cut(summary, room-3) + "..."
			}
		}
		tail := sep + st
		r.line(e, "TOOL", head+summary+tail)
		r.detail("input", pretty(c.Input))
		if ok && res.Content != nil {
			r.detail("output", outputText(res.Content.Output))
		}
	case schema.ToolResult: // a result whose call was not logged
		r.line(e, "RESULT", fmt.Sprintf("%-5s  ->  %s", e.Tool, oneLine(resultSummary(e.Tool, e))))
		r.detail("output", outputText(c.Output))
	case schema.SubagentEnd:
		r.line(e, "SUBAGENT", "finished: "+quote(oneLine(c.Text)))
		r.detail("report", c.Text)
	case schema.TurnEnd:
		r.line(e, "TURN END", "")
	case schema.SessionEnd:
		reason := e.EndReason
		if reason == "" {
			reason = "ended"
		}
		r.line(e, "END", reason)
	case schema.AfrError:
		r.line(e, "NOTE", "afr: "+oneLine(c.Text))
	default:
		r.line(e, "NOTE", e.HookEvent)
	}
}

// toolSummary picks the input field a reviewer needs to see for common
// Claude Code tools, falling back to compact JSON.
func toolSummary(tool string, in json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(in, &m) != nil {
		return compact(in)
	}
	str := func(k string) string { s, _ := m[k].(string); return s }
	switch tool {
	case "Bash":
		if s := str("command"); s != "" {
			return s
		}
	case "Read", "Write", "Edit", "MultiEdit", "NotebookEdit":
		if s := str("file_path"); s != "" {
			return s
		}
	case "Agent", "Task":
		return strings.TrimSpace(str("subagent_type") + ": " + str("description"))
	case "Grep", "Glob":
		if p := str("path"); p != "" {
			return str("pattern") + "  in " + p
		}
		return str("pattern")
	case "WebFetch":
		return str("url")
	case "WebSearch":
		return str("query")
	}
	return compact(in)
}

// resultSummary reduces a tool result to a few words: ok, error, an
// interruption, a background task, plus duration when known.
func resultSummary(tool string, res schema.Event) string {
	var out json.RawMessage
	if res.Content != nil {
		out = res.Content.Output
	}
	s := "ok"
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	switch {
	case res.ToolError:
		msg := strings.TrimSuffix(strings.TrimPrefix(firstLine(outputText(out)), "<tool_use_error>"), "</tool_use_error>")
		s = "error: " + msg
	case m["interrupted"] == true:
		s = "interrupted"
	case m["backgroundTaskId"] != nil:
		s = fmt.Sprintf("running in background (%v)", m["backgroundTaskId"])
	case tool == "Bash":
		if so, _ := m["stdout"].(string); so != "" {
			s = "ok, " + plural(strings.Count(strings.TrimRight(so, "\n"), "\n")+1, "line") + " out"
		}
		if se, _ := m["stderr"].(string); strings.TrimSpace(se) != "" {
			s += ", stderr: " + firstLine(se)
		}
	case m["status"] != nil:
		s = fmt.Sprint(m["status"])
	}
	if res.DurationMS != nil {
		s += fmt.Sprintf("  %s", (time.Duration(*res.DurationMS) * time.Millisecond).String())
	}
	if res.Content != nil && res.Content.Truncated {
		s += fmt.Sprintf("  (output cut from %d bytes)", res.Content.Bytes)
	}
	return s
}

// outputText renders a tool result for reading. Hook results are a
// tool_response object; transcript results are a string or a list of
// content blocks. Both shapes are handled.
func outputText(out json.RawMessage) string {
	if len(out) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(out, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(out, &blocks) == nil && len(blocks) > 0 {
		var parts []string
		for _, b := range blocks {
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	var m map[string]any
	if json.Unmarshal(out, &m) == nil {
		so, _ := m["stdout"].(string)
		se, _ := m["stderr"].(string)
		if so != "" || se != "" {
			if strings.TrimSpace(se) != "" {
				return so + "\n[stderr]\n" + se
			}
			return so
		}
	}
	return pretty(out)
}

// pretty indents JSON for reading, without the \u0026-style escapes Go
// adds for HTML characters, so commands read as typed.
func pretty(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if enc.Encode(v) != nil {
		return string(raw)
	}
	return strings.TrimRight(b.String(), "\n")
}

func compact(raw json.RawMessage) string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if enc.Encode(v) != nil {
		return string(raw)
	}
	return strings.TrimRight(b.String(), "\n")
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// neutralize makes captured text safe to print to a terminal. Control
// characters (ANSI escapes, carriage returns that overwrite a line) and
// bidirectional overrides that reorder text could hide or disguise what
// the reviewer reads, so they are shown as visible escapes instead.
// Newlines and tabs are kept only when keepLines is set.
func neutralize(s string, keepLines bool) string {
	clean := true
	for _, r := range s {
		if unsafeRune(r, keepLines) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case !unsafeRune(r, keepLines):
			b.WriteRune(r)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			fmt.Fprintf(&b, "<U+%04X>", r)
		}
	}
	return b.String()
}

func unsafeRune(r rune, keepLines bool) bool {
	switch {
	case r == '\n' || r == '\t':
		return !keepLines
	case r < 0x20 || r == 0x7f: // C0 controls and DEL, including ESC and CR
		return true
	case r >= 0x80 && r <= 0x9f: // C1 controls
		return true
	case r == 0x200e || r == 0x200f || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069): // bidi controls
		return true
	}
	return false
}

func quote(s string) string { return `"` + s + `"` }

func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// cut shortens s to at most n bytes without splitting a UTF-8 character.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}

// SessionTable prints the session list.
func SessionTable(w io.Writer, rows []Summary, loc *time.Location) error {
	if loc == nil {
		loc = time.Local
	}
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "no sessions recorded yet")
		return err
	}
	_, err := fmt.Fprintf(w, "%-16s  %-8s  %-16s  %5s  %5s  %4s  %-20s  %-30s  %s\n",
		"STARTED", "LAST", "STATUS", "TURNS", "TOOLS", "HIGH", "USER@HOST", "DIR", "SESSION")
	for _, s := range rows {
		if err != nil {
			return err
		}
		last := s.LastSeen.In(loc).Format("15:04:05")
		if s.LastSeen.In(loc).YearDay() != s.Started.In(loc).YearDay() {
			last = s.LastSeen.In(loc).Format("01-02 15:04")
		}
		_, err = fmt.Fprintf(w, "%-16s  %-8s  %-16s  %5d  %5d  %4d  %-20s  %-30s  %s\n",
			s.Started.In(loc).Format("2006-01-02 15:04"), last, cut(s.Status, 16), s.Turns, s.ToolCalls, s.Highlight,
			cut(s.User+"@"+s.Host, 20), cutLeft(s.Cwd, 30), s.Session)
	}
	return err
}

// cutLeft keeps the end of a path, which is the part that identifies it.
func cutLeft(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-(n-3):]
}
