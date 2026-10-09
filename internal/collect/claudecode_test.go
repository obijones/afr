package collect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/obijones/afr/internal/schema"
)

const fixtures = "../../testdata/claude-code/2.1.295"

func convert(t *testing.T, name string) []schema.Event {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fixtures, name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := FromClaudeCode(b, time.Unix(1700000000, 0), Identity{User: "u", Host: "h"})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	evs := c.Events
	for _, e := range evs {
		if e.Schema != schema.Version || e.Session == "" || e.EventID == "" || e.Agent != "claude-code" || e.User != "u" {
			t.Fatalf("%s: base fields missing: %+v", name, e)
		}
	}
	return evs
}

func one(t *testing.T, name, wantType string) schema.Event {
	t.Helper()
	evs := convert(t, name)
	if len(evs) != 1 || evs[0].EventType != wantType {
		t.Fatalf("%s: got %d events, first type %q; want one %q", name, len(evs), evs[0].EventType, wantType)
	}
	return evs[0]
}

func TestClaudeSession(t *testing.T) {
	if e := one(t, "SessionStart-startup", schema.SessionStart); e.Source != "startup" || e.TranscriptPath == "" {
		t.Errorf("startup: source=%q transcript=%q", e.Source, e.TranscriptPath)
	}
	if e := one(t, "SessionStart-resume", schema.SessionStart); e.Source != "resume" {
		t.Errorf("resume: source=%q", e.Source)
	}
	if e := one(t, "SessionEnd", schema.SessionEnd); e.EndReason != "other" {
		t.Errorf("end reason = %q", e.EndReason)
	}
}

func TestClaudePrompt(t *testing.T) {
	e := one(t, "UserPromptSubmit", schema.PromptSubmit)
	if e.TurnID == "" || e.PromptRef != e.Session+"#"+e.TurnID {
		t.Errorf("turn_id=%q prompt_ref=%q", e.TurnID, e.PromptRef)
	}
	if e.Content == nil || !strings.Contains(e.Content.Text, "notes.txt") {
		t.Errorf("prompt text not captured: %+v", e.Content)
	}
}

func TestClaudeToolPair(t *testing.T) {
	call := one(t, "PreToolUse-Bash", schema.ToolCall)
	res := one(t, "PostToolUse-Bash", schema.ToolResult)
	if call.Tool != "Bash" || call.ToolCallID == "" || call.ToolCallID != res.ToolCallID {
		t.Fatalf("pairing: call %q/%q result %q", call.Tool, call.ToolCallID, res.ToolCallID)
	}
	var in struct{ Command string }
	if err := json.Unmarshal(call.Content.Input, &in); err != nil || in.Command != "cat notes.txt" {
		t.Errorf("tool input = %s (%v)", call.Content.Input, err)
	}
	if res.DurationMS == nil || res.Content == nil || len(res.Content.Output) == 0 {
		t.Errorf("result missing duration or output: %+v", res)
	}
	if call.TurnID == "" || call.TurnID != res.TurnID {
		t.Errorf("turn ids differ: %q vs %q", call.TurnID, res.TurnID)
	}
}

func TestClaudeSubagent(t *testing.T) {
	call := one(t, "PreToolUse-Bash-subagent", schema.ToolCall)
	end := one(t, "SubagentStop", schema.SubagentEnd)
	agentRes := one(t, "PostToolUse-Agent", schema.ToolResult)
	if call.AgentID == "" || call.AgentType != "general-purpose" || call.AgentID != end.AgentID {
		t.Fatalf("subagent ids: call %q/%q end %q", call.AgentID, call.AgentType, end.AgentID)
	}
	var out struct {
		AgentID string `json:"agentId"`
	}
	_ = json.Unmarshal(agentRes.Content.Output, &out)
	if out.AgentID != end.AgentID {
		t.Errorf("Agent tool result agentId %q != subagent agent_id %q", out.AgentID, end.AgentID)
	}
	if end.TranscriptPath == "" || end.Content == nil || end.Content.Text == "" {
		t.Errorf("subagent end missing transcript or reply: %+v", end)
	}
}

func TestClaudeStopYieldsReplyAndTurnEnd(t *testing.T) {
	evs := convert(t, "Stop")
	if len(evs) != 2 || evs[0].EventType != schema.AssistantMessage || evs[1].EventType != schema.TurnEnd {
		t.Fatalf("Stop gave %d events", len(evs))
	}
	if evs[0].Content == nil || !strings.HasPrefix(evs[0].Content.Text, "I created") {
		t.Errorf("final reply not captured: %+v", evs[0].Content)
	}
	if evs[0].EventID == evs[1].EventID {
		t.Error("reply and turn end share an event id")
	}
}

func TestClaudeRejectsOtherPayloads(t *testing.T) {
	for _, p := range []string{`{}`, `{"sessionId":"x","toolName":"bash"}`} {
		if _, err := FromClaudeCode([]byte(p), time.Now(), Identity{}); err != ErrNotClaudeCode {
			t.Errorf("%s: err = %v, want ErrNotClaudeCode", p, err)
		}
	}
	if _, err := FromClaudeCode([]byte("not json"), time.Now(), Identity{}); err == nil {
		t.Error("invalid JSON accepted")
	}
}

func TestClaudeLargeOutputCapped(t *testing.T) {
	big := strings.Repeat("x", schema.MaxContentBytes*2)
	p, _ := json.Marshal(map[string]any{
		"hook_event_name": "PostToolUse", "session_id": "s", "tool_name": "Bash", "tool_use_id": "t",
		"tool_response": map[string]string{"stdout": big},
	})
	conv, err := FromClaudeCode(p, time.Now(), Identity{})
	if err != nil {
		t.Fatal(err)
	}
	c := conv.Events[0].Content
	if !c.Truncated || c.Bytes <= schema.MaxContentBytes || len(c.Output) > schema.MaxContentBytes+16 || !json.Valid(c.Output) {
		t.Errorf("cap: truncated=%v bytes=%d len=%d", c.Truncated, c.Bytes, len(c.Output))
	}
}

func TestClaudeTranscriptHints(t *testing.T) {
	for name, want := range map[string]bool{"Stop": true, "SessionEnd": true, "UserPromptSubmit": true, "PreToolUse-Bash": false, "SessionStart-startup": false} {
		b, _ := os.ReadFile(filepath.Join(fixtures, name+".json"))
		c, err := FromClaudeCode(b, time.Now(), Identity{})
		if err != nil {
			t.Fatal(err)
		}
		if c.ReadTranscript != want || c.TranscriptPath == "" {
			t.Errorf("%s: ReadTranscript=%v path=%q", name, c.ReadTranscript, c.TranscriptPath)
		}
		if c.Base.Session == "" || c.Base.EventID != "" {
			t.Errorf("%s: base template %+v", name, c.Base)
		}
	}
}
