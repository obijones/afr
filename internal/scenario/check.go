// Package scenario checks a recorded session against the expectations of
// the seeded scenario (scripts/seeded-scenario.expected.json).
package scenario

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/obijones/afr/internal/schema"
)

// Expect is the expected-results file.
type Expect struct {
	MinRuns               int         `json:"min_runs"`
	MinRepliesPerPrompt   int         `json:"min_replies_per_prompt"`
	MinSubagentEnds       int         `json:"min_subagent_ends"`
	MinRecoveredToolCalls int         `json:"min_recovered_tool_calls"`
	PromptsContain        []string    `json:"prompts_contain"`
	ToolCalls             []ToolCall  `json:"tool_calls"`
	Highlights            []Highlight `json:"highlights"`
}

// ToolCall expects a tool.call whose input contains a substring.
type ToolCall struct {
	Contains string `json:"contains"`
	Tool     string `json:"tool,omitempty"`     // match this tool name if set
	Subagent bool   `json:"subagent,omitempty"` // must come from a subagent
}

// Highlight expects a rule to tag the tool call whose input contains On.
// Not required means pending: reported, never a failure.
type Highlight struct {
	RuleID   string `json:"rule_id"`
	On       string `json:"on"`
	Required bool   `json:"required"`
}

// Load reads an expected-results file.
func Load(path string) (Expect, error) {
	var e Expect
	b, err := os.ReadFile(path)
	if err != nil {
		return e, err
	}
	return e, json.Unmarshal(b, &e)
}

// Result is one check's outcome: PASS, FAIL, or PENDING.
type Result struct {
	Status string
	Check  string
}

// Check runs every expectation against a session's events. integrityOK
// comes from store.Verify on the same log.
func Check(x Expect, events []schema.Event, integrityOK bool) []Result {
	var out []Result
	add := func(ok bool, format string, a ...any) {
		s := "PASS"
		if !ok {
			s = "FAIL"
		}
		out = append(out, Result{s, fmt.Sprintf(format, a...)})
	}

	add(integrityOK, "hash chain verifies")

	var runs, subagents, recovered int
	var prompts []string
	replies := map[string]int{}
	var typedTurns []string
	for _, e := range events {
		switch e.EventType {
		case schema.SessionStart:
			runs++
		case schema.SubagentEnd:
			subagents++
		case schema.PromptSubmit:
			if e.Content != nil {
				prompts = append(prompts, e.Content.Text)
			}
			if e.Origin == "" && e.TurnID != "" {
				typedTurns = append(typedTurns, e.TurnID)
			}
		case schema.AssistantMessage:
			replies[e.TurnID]++
		case schema.ToolCall:
			if e.Origin == schema.OriginTranscript {
				recovered++
			}
		}
	}
	add(runs >= x.MinRuns, "session runs (starts and resumes): %d, want at least %d", runs, x.MinRuns)
	add(subagents >= x.MinSubagentEnds, "subagent ends: %d, want at least %d", subagents, x.MinSubagentEnds)
	add(recovered >= x.MinRecoveredToolCalls, "tool calls recovered from the transcript: %d, want at least %d", recovered, x.MinRecoveredToolCalls)
	for _, t := range typedTurns {
		add(replies[t] >= x.MinRepliesPerPrompt, "turn %s has an AI reply (%d)", t, replies[t])
	}

	for _, want := range x.PromptsContain {
		found := false
		for _, p := range prompts {
			if strings.Contains(p, want) {
				found = true
				break
			}
		}
		add(found, "prompt recorded: %q", want)
	}

	for _, tc := range x.ToolCalls {
		desc := fmt.Sprintf("tool call recorded: %q", tc.Contains)
		if tc.Tool != "" {
			desc += " via " + tc.Tool
		}
		if tc.Subagent {
			desc += " in a subagent"
		}
		add(findCall(events, tc) != nil, "%s", desc)
	}

	for _, h := range x.Highlights {
		call := findCall(events, ToolCall{Contains: h.On})
		tagged := false
		if call != nil {
			for _, got := range call.Highlights {
				tagged = tagged || got.RuleID == h.RuleID
			}
		}
		desc := fmt.Sprintf("highlight %s on %q", h.RuleID, h.On)
		switch {
		case tagged:
			out = append(out, Result{"PASS", desc})
		case !h.Required:
			out = append(out, Result{"PENDING", desc + " (rule not required yet)"})
		default:
			out = append(out, Result{"FAIL", desc})
		}
	}
	return out
}

// Failed reports whether any check failed.
func Failed(rs []Result) bool {
	for _, r := range rs {
		if r.Status == "FAIL" {
			return true
		}
	}
	return false
}

func findCall(events []schema.Event, tc ToolCall) *schema.Event {
	for i, e := range events {
		if e.EventType != schema.ToolCall || e.Content == nil {
			continue
		}
		if tc.Tool != "" && e.Tool != tc.Tool {
			continue
		}
		if tc.Subagent && e.AgentType == "" {
			continue
		}
		if strings.Contains(inputText(e.Content.Input), tc.Contains) {
			return &events[i]
		}
	}
	return nil
}

// inputText renders tool input as JSON without HTML escaping, so "|",
// "&", "<", and ">" match as typed.
func inputText(raw json.RawMessage) string {
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
	return b.String()
}
