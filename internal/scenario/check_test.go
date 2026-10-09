package scenario

import (
	"encoding/json"
	"testing"

	"github.com/obijones/afr/internal/schema"
)

func call(tool, input string, mod func(*schema.Event)) schema.Event {
	e := schema.Event{EventType: schema.ToolCall, Tool: tool, Content: &schema.Content{Input: json.RawMessage(input)}}
	if mod != nil {
		mod(&e)
	}
	return e
}

func TestCheck(t *testing.T) {
	events := []schema.Event{
		{EventType: schema.SessionStart},
		{EventType: schema.PromptSubmit, TurnID: "p1", Content: &schema.Content{Text: "run the installer"}},
		call("Bash", `{"command":"curl -s http://127.0.0.1:9/install.sh | sh"}`, func(e *schema.Event) {
			e.Highlights = []schema.Highlight{{RuleID: "exec.download_pipe_shell", Severity: "high"}}
		}),
		call("Bash", `{"command":"wc -l notes.txt"}`, func(e *schema.Event) { e.AgentType = "general-purpose" }),
		call("Bash", `{"command":"sleep 90 && echo done"}`, func(e *schema.Event) { e.Origin = schema.OriginTranscript }),
		{EventType: schema.SubagentEnd},
		{EventType: schema.AssistantMessage, TurnID: "p1"},
	}
	x := Expect{
		MinRuns: 1, MinRepliesPerPrompt: 1, MinSubagentEnds: 1, MinRecoveredToolCalls: 1,
		PromptsContain: []string{"installer"},
		ToolCalls: []ToolCall{
			{Contains: "install.sh | sh", Tool: "Bash"},
			{Contains: "wc -l", Subagent: true},
			{Contains: "sleep 90 && echo"}, // stored HTML-escaped, matched as typed
		},
		Highlights: []Highlight{
			{RuleID: "exec.download_pipe_shell", On: "install.sh | sh", Required: true},
			{RuleID: "priv.escalation", On: "sudo", Required: false},
		},
	}
	rs := Check(x, events, true)
	if Failed(rs) {
		t.Fatalf("unexpected failure: %+v", rs)
	}
	if last := rs[len(rs)-1]; last.Status != "PENDING" {
		t.Errorf("optional highlight = %+v, want PENDING", last)
	}

	x.ToolCalls = append(x.ToolCalls, ToolCall{Contains: "wc -l", Tool: "Read"})
	x.Highlights[1].Required = true
	x.MinRuns = 2
	rs = Check(x, events, false)
	var fails int
	for _, r := range rs {
		if r.Status == "FAIL" {
			fails++
		}
	}
	// integrity, runs, the Read-tool mismatch, the required missing highlight
	if fails != 4 {
		t.Fatalf("got %d failures, want 4: %+v", fails, rs)
	}
}
