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

func TestWindowsCapture(t *testing.T) {
	load := func(name string) schema.Event {
		t.Helper()
		b, err := os.ReadFile(filepath.Join("../../testdata/claude-code/2.1.296-windows", name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		c, err := FromClaudeCode(b, time.Unix(1700000000, 0), Identity{})
		if err != nil || len(c.Events) == 0 {
			t.Fatalf("%s: %v", name, err)
		}
		return c.Events[len(c.Events)-1]
	}
	if e := load("SessionStart-startup"); e.Model != "claude-opus-5-5" || e.Cwd != `C:\Users\user\afr-test` {
		t.Errorf("session start: model %q cwd %q", e.Model, e.Cwd)
	}
	if e := load("UserPromptSubmit"); e.PermissionMode != "auto" {
		t.Errorf("permission mode %q", e.PermissionMode)
	}
	if e := load("PreToolUse-PowerShell"); e.Tool != "PowerShell" || !strings.Contains(string(e.Content.Input), "New-Item") {
		t.Errorf("PowerShell call: %+v", e)
	}
	if e := load("SubagentStop-suggestion"); e.EventType != schema.SubagentEnd || e.AgentType != "" || e.Content.Text != "add a README to each folder" {
		t.Errorf("suggestion: %+v", e)
	}
}

func TestFailureAndDenialBecomeResults(t *testing.T) {
	conv := func(p string) schema.Event {
		t.Helper()
		c, err := FromClaudeCode([]byte(p), time.Unix(1700000000, 0), Identity{})
		if err != nil || len(c.Events) != 1 {
			t.Fatalf("%v %+v", err, c)
		}
		return c.Events[0]
	}
	// Payload shapes from the Claude Code hooks reference.
	f := conv(`{"session_id":"s","hook_event_name":"PostToolUseFailure","tool_name":"Bash","tool_input":{"command":"npm test"},"tool_use_id":"t1","error":"Exit code 1\nError: Cannot find module 'express'","is_interrupt":false,"duration_ms":4187}`)
	if f.EventType != schema.ToolResult || !f.ToolError || f.ToolCallID != "t1" || *f.DurationMS != 4187 ||
		!strings.Contains(string(f.Content.Output), "Exit code 1") {
		t.Errorf("failure: %+v %s", f, f.Content.Output)
	}
	d := conv(`{"session_id":"s","hook_event_name":"PermissionDenied","permission_mode":"auto","tool_name":"Bash","tool_input":{"command":"rm -rf /tmp/build"},"tool_use_id":"t2","reason":"[Irreversible Local Destruction]"}`)
	var out string
	_ = json.Unmarshal(d.Content.Output, &out)
	if d.EventType != schema.ToolResult || !d.ToolError || d.ToolCallID != "t2" || d.HookEvent != "PermissionDenied" ||
		out != "denied by auto mode: [Irreversible Local Destruction]" {
		t.Errorf("denial: %+v %q", d, out)
	}

	// A real denial from the Claude desktop app (Windows, auto mode).
	b, err := os.ReadFile("../../testdata/claude-code/2.1.295-windows-desktop/PermissionDenied.json")
	if err != nil {
		t.Fatal(err)
	}
	r := conv(string(b))
	out = ""
	_ = json.Unmarshal(r.Content.Output, &out)
	if r.EventType != schema.ToolResult || !r.ToolError || r.Tool != "PowerShell" || r.ToolCallID != "toolu_01J6dwcgxGDkijynsaQzDoAh" ||
		out != "denied by auto mode: [Credential Materialization]" {
		t.Errorf("real denial: %+v %q", r, out)
	}
}
