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

// Highlight expects a rule to tag the tool call whose input contains On,
// at Severity if set. Not required means pending: reported, never a
// failure.
type Highlight struct {
	RuleID   string `json:"rule_id"`
	On       string `json:"on"`
	Severity string `json:"severity,omitempty"`
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

// Result is one check's outcome: PASS, FAIL, PENDING, or SKIPPED.
// SKIPPED means the model never attempted the action (it is absent from
// the harness transcript too), so there was nothing for afr to record.
type Result struct {
	Status string
	Check  string
}

// Check runs every expectation against a session's events. integrityOK
// comes from store.Verify on the same log. attempted holds the input of
// every tool call in the harness transcripts (main and subagents); it
// separates "afr missed it" (in the transcript, not the log: FAIL) from
// "the model never tried" (in neither: SKIPPED). Nil means unknown, and
// every miss is a FAIL.
func Check(x Expect, events []schema.Event, integrityOK bool, attempted []string) []Result {
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
			if e.AgentType == "" {
				break // one of Claude Code's own background tasks, not a subagent
			}
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
		switch {
		case len(findCalls(events, tc)) > 0:
			out = append(out, Result{"PASS", desc})
		case attempted != nil && !anyContains(attempted, tc.Contains):
			out = append(out, Result{"SKIPPED", desc + " (the model did not attempt it)"})
		default:
			out = append(out, Result{"FAIL", desc})
		}
	}

	results := map[string]schema.Event{}
	for _, e := range events {
		if e.EventType == schema.ToolResult && e.ToolCallID != "" {
			results[e.ToolCallID] = e
		}
	}
	for _, h := range x.Highlights {
		// The model may wrap or repeat a command, so any matching call
		// carrying the rule counts.
		calls := findCalls(events, ToolCall{Contains: h.On})
		tagged := false
		var seen []string
		for _, c := range calls {
			hs := append([]schema.Highlight(nil), c.Highlights...)
			if res, ok := results[c.ToolCallID]; ok {
				hs = append(hs, res.Highlights...) // a secret seen in the output
			}
			for _, got := range hs {
				if got.RuleID == h.RuleID {
					seen = append(seen, got.Severity)
					tagged = tagged || h.Severity == "" || got.Severity == h.Severity
				}
			}
		}
		desc := fmt.Sprintf("highlight %s on %q", h.RuleID, h.On)
		if h.Severity != "" {
			desc = fmt.Sprintf("highlight %s %s on %q", h.Severity, h.RuleID, h.On)
		}
		target := fmt.Sprintf("%d matching calls", len(calls))
		switch len(calls) {
		case 0:
			target = "no matching call recorded"
		case 1:
			target = "1 matching call"
		}
		if len(seen) > 0 && !tagged {
			target += ", tagged " + strings.Join(seen, ", ") + " instead"
		}
		switch {
		case tagged:
			out = append(out, Result{"PASS", desc})
		case len(calls) == 0 && attempted != nil && !anyContains(attempted, h.On):
			out = append(out, Result{"SKIPPED", desc + " (the model did not attempt it)"})
		case !h.Required:
			out = append(out, Result{"PENDING", desc + " (rule not required yet; " + target + ")"})
		default:
			out = append(out, Result{"FAIL", desc + " (" + target + ")"})
		}
	}
	return out
}

func anyContains(inputs []string, sub string) bool {
	for _, in := range inputs {
		if strings.Contains(in, sub) {
			return true
		}
	}
	return false
}

// InputText renders tool input JSON as checks match it.
func InputText(raw json.RawMessage) string { return inputText(raw) }

// Failed reports whether any check failed.
func Failed(rs []Result) bool {
	for _, r := range rs {
		if r.Status == "FAIL" {
			return true
		}
	}
	return false
}

// findCalls returns every tool call matching tc.
func findCalls(events []schema.Event, tc ToolCall) []schema.Event {
	var out []schema.Event
	for _, e := range events {
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
			out = append(out, e)
		}
	}
	return out
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
