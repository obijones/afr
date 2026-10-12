package collect

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/obijones/afr/internal/schema"
)

// Identity is who and where the session ran, filled once per hook call.
type Identity struct {
	User string
	Host string
}

// claudePayload holds the Claude Code hook fields afr reads. Field names
// match payloads captured from Claude Code 2.1.295 (testdata/claude-code).
type claudePayload struct {
	HookEventName        string          `json:"hook_event_name"`
	SessionID            string          `json:"session_id"`
	PromptID             string          `json:"prompt_id"`
	TranscriptPath       string          `json:"transcript_path"`
	ScratchpadDir        string          `json:"scratchpad_dir"`
	Cwd                  string          `json:"cwd"`
	Source               string          `json:"source"`
	Reason               string          `json:"reason"`
	PermissionMode       string          `json:"permission_mode"`
	Prompt               string          `json:"prompt"`
	ToolName             string          `json:"tool_name"`
	ToolUseID            string          `json:"tool_use_id"`
	ToolInput            json.RawMessage `json:"tool_input"`
	ToolResponse         json.RawMessage `json:"tool_response"`
	DurationMS           *int64          `json:"duration_ms"`
	AgentID              string          `json:"agent_id"`
	AgentType            string          `json:"agent_type"`
	AgentTranscriptPath  string          `json:"agent_transcript_path"`
	LastAssistantMessage string          `json:"last_assistant_message"`
	Model                string          `json:"model"`        // SessionStart only, not always present
	Error                string          `json:"error"`        // PostToolUseFailure
	IsInterrupt          bool            `json:"is_interrupt"` // PostToolUseFailure
}

// ErrNotClaudeCode means the payload lacks the fields every Claude Code
// hook payload carries.
var ErrNotClaudeCode = errors.New("payload is not a Claude Code hook payload")

// Converted is one hook payload turned into afr events, plus what the
// transcript reader needs to fill the gaps hooks leave.
type Converted struct {
	Events         []schema.Event // unsealed; the store sets seq and hashes
	Base           schema.Event   // shared fields, a template for recovered events
	TranscriptPath string
	ReadTranscript bool   // this hook is a point to catch up on the transcript
	Scratchpad     string // the harness's per-session scratch folder, if the payload names one
	Reply          string // final reply text carried by Stop, if any
}

// FromClaudeCode converts one Claude Code hook payload. Stop yields two
// events: the final reply and the turn end.
func FromClaudeCode(payload []byte, now time.Time, id Identity) (*Converted, error) {
	var p claudePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, err
	}
	if p.SessionID == "" || p.HookEventName == "" {
		return nil, ErrNotClaudeCode
	}

	base := schema.Event{
		Schema:    schema.Version,
		EventID:   schema.NewEventID(now),
		TS:        now.UTC(),
		Session:   p.SessionID,
		TurnID:    p.PromptID,
		AgentID:   p.AgentID,
		AgentType: p.AgentType,
		User:      id.User,
		Host:      id.Host,
		Agent:     "claude-code",
		Cwd:       p.Cwd,
		HookEvent: p.HookEventName,
	}
	if p.PromptID != "" {
		base.PromptRef = p.SessionID + "#" + p.PromptID
	}

	c := &Converted{Base: base, TranscriptPath: p.TranscriptPath, Scratchpad: p.ScratchpadDir}
	c.Base.EventID = ""
	c.Base.HookEvent = ""

	e := base
	switch p.HookEventName {
	case "SessionStart":
		e.EventType = schema.SessionStart
		e.Source = p.Source
		e.Model = p.Model
		e.PermissionMode = p.PermissionMode
		e.TranscriptPath = p.TranscriptPath
	case "UserPromptSubmit":
		c.ReadTranscript = true // catch up on the end of the previous turn
		e.EventType = schema.PromptSubmit
		e.PermissionMode = p.PermissionMode
		e.Content = textContent(p.Prompt)
	case "PreToolUse":
		e.EventType = schema.ToolCall
		e.Tool, e.ToolCallID = p.ToolName, p.ToolUseID
		e.Content = jsonContent(p.ToolInput, nil)
	case "PostToolUse":
		e.EventType = schema.ToolResult
		e.Tool, e.ToolCallID = p.ToolName, p.ToolUseID
		e.DurationMS = p.DurationMS
		e.Content = jsonContent(nil, p.ToolResponse)
	case "PostToolUseFailure":
		// PostToolUse fires only on success; a failed call reports here.
		e.EventType = schema.ToolResult
		e.Tool, e.ToolCallID = p.ToolName, p.ToolUseID
		e.DurationMS = p.DurationMS
		e.ToolError = true
		msg := p.Error
		if p.IsInterrupt {
			msg = "interrupted: " + msg
		}
		e.Content = jsonContent(nil, jsonString(msg))
	case "PermissionDenied":
		// Auto mode refused the call before it ran; no PostToolUse follows.
		e.EventType = schema.ToolResult
		e.Tool, e.ToolCallID = p.ToolName, p.ToolUseID
		e.ToolError = true
		e.Content = jsonContent(nil, jsonString("denied by auto mode: "+p.Reason))
	case "Stop":
		c.ReadTranscript = true
		e.EventType = schema.TurnEnd
		if p.LastAssistantMessage != "" {
			c.Reply = p.LastAssistantMessage
			reply := base
			reply.EventType = schema.AssistantMessage
			reply.Content = textContent(p.LastAssistantMessage)
			end := e
			end.EventID = schema.NewEventID(now)
			c.Events = []schema.Event{reply, end}
			return c, nil
		}
	case "SubagentStop":
		e.EventType = schema.SubagentEnd
		e.TranscriptPath = p.AgentTranscriptPath
		e.Content = textContent(p.LastAssistantMessage)
	case "SessionEnd":
		c.ReadTranscript = true
		e.EventType = schema.SessionEnd
		e.EndReason = p.Reason
	default:
		e.EventType = schema.HarnessOther
	}
	c.Events = []schema.Event{e}
	return c, nil
}

func jsonString(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

func textContent(s string) *schema.Content {
	if s == "" {
		return nil
	}
	text, n, cut := schema.CapText(s)
	return &schema.Content{Text: text, Truncated: cut, Bytes: n}
}

func jsonContent(in, out json.RawMessage) *schema.Content {
	if len(in) == 0 && len(out) == 0 {
		return nil
	}
	c := &schema.Content{}
	var n1, n2 int
	var cut1, cut2 bool
	if len(in) > 0 {
		c.Input, n1, cut1 = schema.CapJSON(in)
	}
	if len(out) > 0 {
		c.Output, n2, cut2 = schema.CapJSON(out)
	}
	c.Truncated = cut1 || cut2
	c.Bytes = max(n1, n2)
	return c
}
