// Package schema defines afr's normalized event, the contract between
// harness adapters and every reviewer view. Changes that break readers
// must bump Version.
package schema

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// Version is the value written to every event's schema field.
const Version = "1"

// OriginTranscript marks an event recovered from the harness transcript.
const OriginTranscript = "transcript"

// Event types.
const (
	SessionStart     = "session.start"
	PromptSubmit     = "prompt.submit"
	AssistantMessage = "assistant.message"
	ToolCall         = "tool.call"
	ToolResult       = "tool.result"
	SubagentEnd      = "subagent.end"
	TurnEnd          = "turn.end"
	SessionEnd       = "session.end"
	HarnessOther     = "harness.other"  // a hook afr records but does not interpret
	HarnessNotice    = "harness.notice" // a notice the harness wrote: a safeguard stop, a model fallback, a version change
	AfrError         = "afr.error"
)

// MaxContentBytes caps each content field (text, input, output). Larger
// values are cut, Truncated is set, and Bytes keeps the original size.
// The default is to be revisited once testing shows how often it is hit.
const MaxContentBytes = 64 << 10

// Event is one moment in an AI session. Field order is fixed: Hash is
// computed over the JSON encoding, so reordering fields changes hashes.
type Event struct {
	Schema    string    `json:"schema"`
	EventID   string    `json:"event_id"`
	TS        time.Time `json:"ts"`
	Seq       int64     `json:"seq"`
	Session   string    `json:"session"`
	TurnID    string    `json:"turn_id,omitempty"`
	PromptRef string    `json:"prompt_ref,omitempty"`
	AgentID   string    `json:"agent_id,omitempty"`
	AgentType string    `json:"agent_type,omitempty"`
	User      string    `json:"user"`
	Host      string    `json:"host"`
	Agent     string    `json:"agent"`
	Model     string    `json:"model,omitempty"` // the model that wrote an AI reply
	// AgentVersion is the harness version, from its transcript; empty
	// until the first transcript read in a session.
	AgentVersion string `json:"agent_version,omitempty"`
	Cwd          string `json:"cwd,omitempty"`
	EventType    string `json:"event_type"`
	HookEvent    string `json:"hook_event,omitempty"`
	// Origin is "transcript" for events recovered from the harness
	// transcript rather than a hook; empty means a hook.
	Origin         string `json:"origin,omitempty"`
	TranscriptUUID string `json:"transcript_uuid,omitempty"`

	Tool       string `json:"tool,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	DurationMS *int64 `json:"duration_ms,omitempty"`
	ToolError  bool   `json:"tool_error,omitempty"` // the tool call failed or was refused

	Source         string  `json:"source,omitempty"`     // session.start: startup, resume, ...
	EndReason      string  `json:"end_reason,omitempty"` // session.end
	TranscriptPath string  `json:"transcript_path,omitempty"`
	Notice         *Notice `json:"notice,omitempty"` // harness.notice

	Content     *Content    `json:"content,omitempty"`
	Highlights  []Highlight `json:"highlights,omitempty"`
	ContentHash string      `json:"content_hash,omitempty"`
	PrevHash    string      `json:"prev_hash"`
	Hash        string      `json:"hash"`
}

// Content holds what was said or done, verbatim.
type Content struct {
	Text      string          `json:"text,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	Output    json.RawMessage `json:"output,omitempty"`
	Truncated bool            `json:"truncated,omitempty"`
	Bytes     int             `json:"bytes,omitempty"` // original size of the largest cut field
}

// Notice is the structured part of a harness.notice event; its message
// is in Content.Text.
type Notice struct {
	Kind          string `json:"kind"`                     // harness subtype, or "agent_version_change"
	Level         string `json:"level,omitempty"`          // notice, warning, error
	Category      string `json:"category,omitempty"`       // refusal category, such as "cyber"
	Explanation   string `json:"explanation,omitempty"`    // refusal explanation from the API
	OriginalModel string `json:"original_model,omitempty"` // model fallback: from
	FallbackModel string `json:"fallback_model,omitempty"` // model fallback: to
	Trigger       string `json:"trigger,omitempty"`
	Scope         string `json:"scope,omitempty"`
}

// Highlight tags an event for reviewers. It never blocks anything.
type Highlight struct {
	RuleID   string `json:"rule_id"`
	Severity string `json:"severity"`
}

// NewEventID returns a time-ordered unique id: 12 hex digits of Unix
// milliseconds followed by 20 random hex digits.
func NewEventID(now time.Time) string {
	var r [10]byte
	_, _ = rand.Read(r[:])
	return fmt.Sprintf("%012x%s", now.UnixMilli(), hex.EncodeToString(r[:]))
}

// Seal sets ContentHash, Seq, PrevHash, and Hash. The hash covers every
// field except Content (represented by ContentHash) and Hash itself, so
// content can later be purged without breaking the chain.
func (e *Event) Seal(seq int64, prevHash string) error {
	e.Seq = seq
	e.PrevHash = prevHash
	e.ContentHash = ""
	if e.Content != nil {
		h, err := e.ContentDigest()
		if err != nil {
			return err
		}
		e.ContentHash = h
	}
	h, err := e.ComputeHash()
	if err != nil {
		return err
	}
	e.Hash = h
	return nil
}

// ContentDigest hashes the JSON encoding of Content. Re-encoding content
// read back from a log yields the same bytes, so Verify can recompute it.
func (e Event) ContentDigest() (string, error) {
	if e.Content == nil {
		return "", nil
	}
	b, err := json.Marshal(e.Content)
	if err != nil {
		return "", err
	}
	return sum(b), nil
}

// ComputeHash returns the hash Seal would store, for verification.
func (e Event) ComputeHash() (string, error) {
	e.Content = nil
	e.Hash = ""
	b, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	return sum(b), nil
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// CapText cuts s to MaxContentBytes on a UTF-8 boundary. It reports the
// original length when it cut.
func CapText(s string) (string, int, bool) {
	if len(s) <= MaxContentBytes {
		return s, 0, false
	}
	cut := MaxContentBytes
	for cut > 0 && !utf8RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], len(s), true
}

// CapJSON keeps raw JSON as is when small enough. Otherwise it returns
// the first MaxContentBytes of it as a JSON string, so the log line stays
// valid JSON, and reports the original length.
func CapJSON(raw json.RawMessage) (json.RawMessage, int, bool) {
	if len(raw) <= MaxContentBytes {
		return raw, 0, false
	}
	cut, n, _ := CapText(string(raw))
	b, _ := json.Marshal(cut)
	return b, n, true
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }
