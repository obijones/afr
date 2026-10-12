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

	// Current holds highlights today's rules give that the log does not
	// have, by event id (see Rescan). They are marked with * in the replay.
	Current map[string][]schema.Highlight
}

// Replay prints one session as a conversation, start to end, in the
// order things happened. Each tool call is shown with its result.
// integrity is a one-line verdict from the verifier, shown in the header.
func Replay(w io.Writer, events []schema.Event, integrity string, o Options) error {
	m, err := Build(events, o.Current)
	if err != nil {
		return err
	}
	if o.Width == 0 {
		o.Width = 110
	}
	if o.Loc == nil {
		o.Loc = time.Local
	}
	sum := m.Summary
	models := strings.Join(sum.Models, ", ")
	if models == "" {
		models = "unknown"
	}
	agent := sum.Agent
	if len(sum.Versions) > 0 {
		agent += " " + strings.Join(sum.Versions, " -> ")
	}
	r := &replayer{w: w, o: o}
	// The header is captured data too (folder, user, models, a verifier
	// error), so it is neutralized like every line.
	r.write(neutralize(fmt.Sprintf("SESSION %s  %s@%s  %s  %s  model: %s", sum.Session, sum.User, sum.Host, sum.Cwd, agent, models), false) + "\n")
	r.write(neutralize(fmt.Sprintf("%s  %s, %s, %s, %s  integrity: %s", sum.Started.In(o.Loc).Format("2006-01-02 MST"),
		plural(sum.Turns, "turn"), plural(sum.ToolCalls, "tool call"), plural(sum.Highlight, "highlight"),
		plural(sum.Notices, "harness notice"), integrity), false) + "\n")
	if m.CurrentCount > 0 {
		fmt.Fprintf(w, "%s marked * come from the current highlight rules, not from when the session was recorded\n",
			plural(m.CurrentCount, "highlight"))
	}
	fmt.Fprintln(w)
	for _, l := range m.Lines {
		r.line(l)
	}
	return r.err
}

type replayer struct {
	w   io.Writer
	o   Options
	err error
}

// prefixFor builds the start of a line: time, kind, turn, then any
// highlights and the subagent tag. Built without a format string, since
// rule ids and agent types come from captured data.
func (r *replayer) prefixFor(l Line) string {
	turn := l.Turn
	if turn == "" {
		turn = "-"
	}
	p := fmt.Sprintf("%s  %-9s %-3s ", l.Event.TS.In(r.o.Loc).Format("15:04:05"), l.Kind, turn)
	// Highlights go first, so a long line can never cut them off.
	for _, h := range l.Marks {
		p += "[" + strings.ToUpper(h.Severity) + " " + h.Label() + "] "
	}
	if l.Event.AgentType != "" {
		p += "[" + l.Event.AgentType + "] "
	}
	return p
}

func (r *replayer) line(l Line) {
	text := l.Text
	if t := l.Tool; t != nil {
		// Fit both command and status: when the line is too long, the
		// status gets at most about 40% of the room, the command the rest.
		head := fmt.Sprintf("%-5s  ", t.Name)
		const sep = "  ->  "
		summary, st := t.Summary, t.Status
		avail := r.o.Width - len(neutralize(r.prefixFor(l), false)) - len(head) - len(sep)
		if len(summary)+len(st) > avail {
			if maxSt := max(avail*2/5, 24); len(st) > maxSt {
				st = cut(st, maxSt-3) + "..."
			}
			if room := max(avail-len(st), 20); len(summary) > room {
				summary = cut(summary, room-3) + "..."
			}
		}
		text = head + summary + sep + st
	}
	s := neutralize(r.prefixFor(l)+text, false)
	if len(s) > r.o.Width {
		s = cut(s, r.o.Width-3) + "..."
	}
	r.write(s + "\n")
	var why []string
	for _, h := range l.Marks {
		if h.Detail != "" {
			why = append(why, strings.ToUpper(h.Severity)+" "+h.Label()+": "+h.Detail)
		}
	}
	r.detail("highlights", strings.Join(why, "\n"))
	for _, d := range l.Details {
		r.detail(d.Label, d.Body)
	}
}

// detail prints a full value under its line, indented, in --full mode.
func (r *replayer) detail(label, body string) {
	if !r.o.Full || strings.TrimSpace(body) == "" {
		return
	}
	r.write("          " + label + ":\n")
	// Windows line endings are line breaks; a lone \r stays visible,
	// since on a terminal it can overwrite text already printed.
	body = strings.ReplaceAll(body, "\r\n", "\n")
	for _, l := range strings.Split(strings.TrimRight(neutralize(body, true), "\n"), "\n") {
		r.write("            " + l + "\n")
	}
}

func (r *replayer) write(s string) {
	if r.err == nil {
		_, r.err = io.WriteString(r.w, s)
	}
}

// isHelper reports a subagent end that came from one of Claude Code's
// own background tasks: those carry no agent type, while subagents the
// model launches with the Agent tool do.
func isHelper(e schema.Event) bool {
	return e.EventType == schema.SubagentEnd && e.AgentType == ""
}

// noticeSummary puts what a reviewer needs first: level, and for a model
// fallback the category and the switch, then the harness's own words.
func noticeSummary(e schema.Event) string {
	n := e.Notice
	if n == nil {
		n = &schema.Notice{}
	}
	text := ""
	if e.Content != nil {
		text = oneLine(e.Content.Text)
	}
	level := strings.ToUpper(n.Level)
	if level == "" {
		level = "NOTICE"
	}
	s := "[" + level + "] "
	switch {
	case n.Kind == "model_refusal_fallback":
		s += "safeguards flagged this"
		if n.Category != "" {
			s += " (" + n.Category + ")"
		}
		if n.OriginalModel != "" || n.FallbackModel != "" {
			s += ": " + n.OriginalModel + " -> " + n.FallbackModel
		}
		if n.Scope != "" {
			s += " for the " + n.Scope
		}
	default:
		s += text
	}
	return s
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
	case "Bash", "PowerShell", "Monitor":
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
	case "SubagentHandback":
		if s := str("message"); s != "" {
			return s
		}
	case "mcp__ccd_directory__change_directory": // the desktop app moving the session (F-012)
		return "move the session to " + str("path")
	case "mcp__ccd_directory__request_directory":
		if p := str("path"); p != "" {
			return "ask the user for access to " + p
		}
		return "ask the user to pick a folder"
	}
	return compact(in)
}

// relativeTo shows a path under the session's working directory relative
// to it, so the part that identifies the file is not cut off. Anything
// else, including commands, is returned unchanged.
func relativeTo(cwd, s string) string {
	if cwd == "" || !strings.HasPrefix(s, cwd) {
		return s
	}
	rest := s[len(cwd):]
	if len(rest) > 1 && (rest[0] == '/' || rest[0] == '\\') {
		return rest[1:]
	}
	return s
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
	case res.ToolError && res.HookEvent == "PermissionDenied":
		s = firstLine(outputText(out))
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
		return strings.TrimRight(s[:i], "\r")
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

// SessionTable prints the session list. Session ids are cut to 8
// characters, which replay, verify, report, and export accept as a
// prefix, unless fullIDs is set.
func SessionTable(w io.Writer, rows []Summary, loc *time.Location, fullIDs bool) error {
	if loc == nil {
		loc = time.Local
	}
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "no sessions recorded yet")
		return err
	}
	_, err := fmt.Fprintf(w, "%-16s  %-8s  %-16s  %5s  %5s  %-8s  %6s  %-18s  %-20s  %-30s  %s\n",
		"STARTED", "LAST", "STATUS", "TURNS", "TOOLS", "H/M/L", "NOTICE", "MODEL", "USER@HOST", "DIR", "SESSION")
	for _, s := range rows {
		if err != nil {
			return err
		}
		last := s.LastSeen.In(loc).Format("15:04:05")
		if s.LastSeen.In(loc).YearDay() != s.Started.In(loc).YearDay() {
			last = s.LastSeen.In(loc).Format("01-02 15:04")
		}
		model := "-"
		switch len(s.Models) {
		case 0:
		case 1:
			model = s.Models[0]
		default:
			model = s.Models[len(s.Models)-1] + "+"
		}
		dir := s.LastCwd // where the session is now, after any move
		if dir == "" {
			dir = s.Cwd
		}
		id := s.Session
		if !fullIDs {
			id = cut(id, 8)
		}
		sev := fmt.Sprintf("%d/%d/%d", s.Severity[0], s.Severity[1], s.Severity[2])
		// Every column but the times and counts comes from captured data.
		n := func(v string) string { return neutralize(v, false) }
		_, err = fmt.Fprintf(w, "%-16s  %-8s  %-16s  %5d  %5d  %-8s  %6d  %-18s  %-20s  %-30s  %s\n",
			s.Started.In(loc).Format("2006-01-02 15:04"), last, cut(n(s.Status), 16), s.Turns, s.ToolCalls, sev,
			s.Notices, cut(n(model), 18), cut(n(s.User+"@"+s.Host), 20), cutLeft(n(dir), 30), n(id))
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
