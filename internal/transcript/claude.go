// Package transcript reads the conversation transcript a harness writes,
// to recover what its hooks do not carry: AI replies between tool calls,
// tool calls the harness rejected before any hook ran, and messages the
// harness injected itself.
package transcript

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"
)

// Kind is what a transcript record holds.
type Kind int

const (
	Prompt     Kind = iota + 1 // a user (or harness-injected) message that starts a turn
	Text                       // AI reply text
	ToolUse                    // AI request to run a tool
	ToolResult                 // what the tool returned
	Skipped                    // a line too large to read; Bytes says how large
	Notice                     // a harness notice: a safeguard stop, a model fallback, a warning
)

// NoticeInfo is the structured part of a harness notice. Fields are empty
// when the notice does not carry them.
type NoticeInfo struct {
	Subtype       string // Claude Code's system subtype, such as "model_refusal_fallback"
	Level         string // notice, warning, error
	Category      string // refusal category, such as "cyber"
	Explanation   string
	OriginalModel string
	FallbackModel string
	Trigger       string
	Scope         string
}

// Record is one item recovered from a transcript. One transcript line can
// yield several records (an assistant message with text and tool calls).
type Record struct {
	Kind     Kind
	UUID     string
	TS       time.Time
	PromptID string // set on user records only; assistant records lack it
	Text     string
	ToolID   string
	ToolName string
	Input    json.RawMessage
	Output   json.RawMessage
	IsError  bool
	Bytes    int64  // Skipped only
	Model    string // assistant records: the model that produced them
	Version  string // the harness version that wrote the record
	Meta     bool   // a message the harness wrote into the conversation itself
	Cwd      string // the session's working folder when the record was written
	Notice   *NoticeInfo
}

// MaxLine is the largest single transcript line ReadFrom will parse;
// longer lines (a huge base64 file read, say) are skipped and reported.
const MaxLine = 8 << 20

// ReadFrom parses complete lines of a Claude Code transcript starting at
// offset, reading about max bytes, and returns the offset to resume from.
// If one line is longer than max it is read on its own, up to MaxLine. A
// line still being written (no trailing newline) is left for the next
// call. If the file is now shorter than offset, it was replaced, and
// reading restarts at 0.
func ReadFrom(path string, offset, max int64) ([]Record, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, offset, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, offset, err
	}
	if info.Size() < offset {
		offset = 0
	}
	buf := make([]byte, min(max, info.Size()-offset))
	n, err := f.ReadAt(buf, offset)
	if err != nil && err != io.EOF {
		return nil, offset, err
	}
	buf = buf[:n]
	end := bytes.LastIndexByte(buf, '\n')
	if end < 0 {
		if int64(n) < max {
			return nil, offset, nil // a line still being written
		}
		// One line fills the whole window: read it alone if it is not too
		// large, otherwise skip past it rather than stall on it forever.
		next, ok, err := nextLineStart(f, offset+int64(n))
		if err != nil || !ok {
			return nil, offset, err
		}
		size := next - offset
		if size > MaxLine {
			return []Record{{Kind: Skipped, Bytes: size}}, next, nil
		}
		whole := make([]byte, size-1)
		if _, err := f.ReadAt(whole, offset); err != nil {
			return nil, offset, err
		}
		return parseLine(whole), next, nil
	}
	var recs []Record
	for _, line := range bytes.Split(buf[:end], []byte("\n")) {
		recs = append(recs, parseLine(line)...)
	}
	return recs, offset + int64(end) + 1, nil
}

// nextLineStart scans forward from pos for the next newline and returns
// the offset just past it. ok is false if the file ends first.
func nextLineStart(f *os.File, pos int64) (int64, bool, error) {
	buf := make([]byte, 1<<20)
	for {
		n, err := f.ReadAt(buf, pos)
		if i := bytes.IndexByte(buf[:n], '\n'); i >= 0 {
			return pos + int64(i) + 1, true, nil
		}
		pos += int64(n)
		if err == io.EOF {
			return 0, false, nil
		}
		if err != nil {
			return 0, false, err
		}
	}
}

type line struct {
	Type           string    `json:"type"`
	UUID           string    `json:"uuid"`
	Timestamp      time.Time `json:"timestamp"`
	PromptID       string    `json:"promptId"`
	IsSidechain    bool      `json:"isSidechain"`
	RequestedModel string    `json:"requestedModel"`
	Version        string    `json:"version"`
	IsMeta         bool      `json:"isMeta"`
	Cwd            string    `json:"cwd"`

	// system records
	Subtype               string          `json:"subtype"`
	Level                 string          `json:"level"`
	Content               json.RawMessage `json:"content"`
	Trigger               string          `json:"trigger"`
	Scope                 string          `json:"scope"`
	OriginalModel         string          `json:"originalModel"`
	FallbackModel         string          `json:"fallbackModel"`
	APIRefusalCategory    string          `json:"apiRefusalCategory"`
	APIRefusalExplanation string          `json:"apiRefusalExplanation"`

	Message struct {
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// systemNotice keeps system records a reviewer should see: a model
// fallback always, and anything at notice, warning, or error level.
// Routine records (per-turn hook summaries at "suggestion" level) are
// dropped.
func systemNotice(l line, base Record) []Record {
	switch {
	case l.Subtype == "model_refusal_fallback":
	case l.Level == "notice", l.Level == "warning", l.Level == "error":
	default:
		return nil
	}
	var text string
	_ = json.Unmarshal(l.Content, &text)
	r := base
	r.Kind, r.Text = Notice, text
	r.Notice = &NoticeInfo{
		Subtype: l.Subtype, Level: l.Level, Category: l.APIRefusalCategory, Explanation: l.APIRefusalExplanation,
		OriginalModel: l.OriginalModel, FallbackModel: l.FallbackModel, Trigger: l.Trigger, Scope: l.Scope,
	}
	return []Record{r}
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// parseLine maps one transcript line to records. Lines afr does not
// need (attachments, routine system records, thinking, subagent
// sidechains, bad JSON) yield nothing.
func parseLine(b []byte) []Record {
	var l line
	if json.Unmarshal(b, &l) != nil || l.IsSidechain {
		return nil
	}
	base := Record{UUID: l.UUID, TS: l.Timestamp, PromptID: l.PromptID, Version: l.Version, Meta: l.IsMeta, Cwd: l.Cwd}
	if l.Type == "system" {
		return systemNotice(l, base)
	}
	if l.Type != "user" && l.Type != "assistant" {
		return nil
	}
	if l.Type == "assistant" {
		base.Model = l.Message.Model
		if base.Model == "" || base.Model == "<synthetic>" { // Claude Code's placeholder for messages it writes itself
			base.Model = l.RequestedModel
		}
		if base.Model == "<synthetic>" {
			base.Model = ""
		}
	}

	var s string
	if json.Unmarshal(l.Message.Content, &s) == nil {
		if l.Type == "user" && s != "" {
			r := base
			r.Kind, r.Text = Prompt, s
			return []Record{r}
		}
		return nil
	}
	var blocks []block
	if json.Unmarshal(l.Message.Content, &blocks) != nil {
		return nil
	}

	var out []Record
	var userText []string
	for _, bl := range blocks {
		r := base
		switch {
		case l.Type == "assistant" && bl.Type == "text" && bl.Text != "":
			r.Kind, r.Text = Text, bl.Text
		case l.Type == "assistant" && bl.Type == "tool_use":
			r.Kind, r.ToolID, r.ToolName, r.Input = ToolUse, bl.ID, bl.Name, bl.Input
		case l.Type == "user" && bl.Type == "tool_result":
			r.Kind, r.ToolID, r.Output, r.IsError = ToolResult, bl.ToolUseID, bl.Content, bl.IsError
		case l.Type == "user" && bl.Type == "text":
			userText = append(userText, bl.Text)
			continue
		default:
			continue
		}
		out = append(out, r)
	}
	if len(userText) > 0 && len(out) == 0 {
		r := base
		r.Kind, r.Text = Prompt, strings.Join(userText, "\n")
		out = append(out, r)
	}
	return out
}
