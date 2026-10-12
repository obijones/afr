package rules

import (
	"encoding/json"
	"slices"

	"github.com/obijones/afr/internal/schema"
)

// Workspace tracks the folders a session may write to without a write
// being "outside": the first working folder seen, folders the Claude
// desktop app moved the session to, and the scratchpad the harness gives
// the session. Recording and replay both build it with Observe, event by
// event in time order, so they always agree.
//
// A folder the agent reaches with cd does not count: only a move the app
// performs (its change_directory tool, which the user asks for) does.
type Workspace struct {
	Root       string            `json:"root,omitempty"`       // first working folder seen
	Moved      []string          `json:"moved,omitempty"`      // folders the session was moved to
	Scratchpad string            `json:"scratchpad,omitempty"` // the harness's per-session scratchpad
	Pending    map[string]string `json:"pending,omitempty"`    // change_directory calls awaiting a result: call id -> path
}

// moveTool is the desktop app's tool that moves a session to another
// folder (docs/findings.md F-012).
const moveTool = "mcp__ccd_directory__change_directory"

// Observe updates the workspace from one event.
func (w *Workspace) Observe(e schema.Event) {
	if w.Root == "" && e.Cwd != "" {
		w.Root = e.Cwd
	}
	if e.ScratchpadDir != "" {
		w.Scratchpad = e.ScratchpadDir
	}
	if e.ToolCallID == "" {
		return
	}
	switch e.EventType {
	case schema.ToolCall:
		if e.Tool != moveTool {
			return
		}
		var in struct {
			Path string `json:"path"`
		}
		if e.Content != nil && json.Unmarshal(e.Content.Input, &in) == nil && in.Path != "" {
			if w.Pending == nil {
				w.Pending = map[string]string{}
			}
			w.Pending[e.ToolCallID] = in.Path
		}
	case schema.ToolResult: // matched by call id: results recovered from a transcript carry no tool name
		p, ok := w.Pending[e.ToolCallID]
		if !ok {
			return
		}
		delete(w.Pending, e.ToolCallID)
		if !e.ToolError && !slices.Contains(w.Moved, p) {
			w.Moved = append(w.Moved, p)
		}
	}
}

// Apply sets a rules context's workspace folders.
func (w *Workspace) Apply(c *Context) {
	c.Workspace = w.Root
	c.Workspaces = append([]string(nil), w.Moved...)
	if w.Scratchpad != "" {
		c.Workspaces = append(c.Workspaces, w.Scratchpad)
	}
}
