// Package session records a hook call into its session log, filling in
// what hooks miss from the harness transcript, and keeps the small state
// that makes that possible across hook processes.
package session

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/obijones/afr/internal/collect"
	"github.com/obijones/afr/internal/platform"
	"github.com/obijones/afr/internal/rules"
	"github.com/obijones/afr/internal/schema"
	"github.com/obijones/afr/internal/store"
	"github.com/obijones/afr/internal/transcript"
)

// maxTranscriptRead bounds one catch-up read to keep each hook call well
// inside the latency budget; a backlog is worked through over the next
// catch-up points. (A 5 MB first read measured p95 107 ms.)
const maxTranscriptRead = 1 << 20

// maxFinalRead is the larger read allowed at session end, so a backlog is
// not left behind; a short pause at exit holds up no tool call.
const maxFinalRead = 8 << 20

// maxIDs caps each remembered id list. Ids that never match (a subagent's
// tool calls, which live in its own transcript) age out.
const maxIDs = 2000

// State is kept per session beside the log, read and written under the
// session lock.
type State struct {
	TranscriptPath string `json:"transcript_path"`
	Offset         int64  `json:"offset"`
	PromptID       string `json:"prompt_id"` // current turn while scanning the transcript
	Model          string `json:"model"`     // latest model seen in the transcript
	Version        string `json:"version"`   // latest harness version seen in the transcript
	Workspace      string `json:"workspace"` // first working directory seen: the project root for rules
	Mode           string `json:"mode"`      // latest permission mode seen, for rules

	HookToolIDs       []string `json:"hook_tool_ids"`       // tool calls hooks recorded, not yet seen in the transcript
	HookPromptIDs     []string `json:"hook_prompt_ids"`     // prompts hooks recorded
	TranscriptToolIDs []string `json:"transcript_tool_ids"` // tool calls recovered from the transcript, awaiting results
	PendingReplies    []string `json:"pending_replies"`     // hashes of final replies logged from Stop before the transcript had them

	// seen holds transcript uuids already in the log. Filled only when
	// state is rebuilt from the log, for the re-read that follows.
	seen map[string]bool
}

func statePath(dataDir, session string) string {
	return strings.TrimSuffix(store.LogPath(dataDir, session), ".ndjson") + ".state.json"
}

// Warn receives problems that must not stop recording, such as a
// highlight rule that failed. The hook sends them to afr's error log.
var Warn = func(string) {}

// Record writes one converted hook call. At catch-up points (prompt,
// turn end, session end) it first reads new transcript lines and adds
// events for replies, tool calls, and messages that no hook carried.
func Record(dataDir string, c *collect.Converted) error {
	if len(c.Events) == 0 {
		return nil
	}
	session := c.Events[0].Session
	return store.WithSession(dataDir, session, func(w *store.Writer) error {
		st, ok := load(statePath(dataDir, session))
		if c.TranscriptPath != "" {
			st.TranscriptPath = c.TranscriptPath
		}
		// Without state, or if the transcript was replaced, the next read
		// starts at 0. Rebuild what was already logged first, so nothing
		// is recorded twice.
		if w.Seq() > 0 && (!ok || transcriptShrank(st)) {
			if err := st.seedFromLog(store.LogPath(dataDir, session)); err != nil {
				return err
			}
		}
		events := c.Events
		for _, e := range events {
			switch e.EventType {
			case schema.ToolCall:
				st.HookToolIDs = remember(st.HookToolIDs, e.ToolCallID)
			case schema.PromptSubmit:
				st.HookPromptIDs = remember(st.HookPromptIDs, e.TurnID)
			}
		}

		var recovered []schema.Event
		if c.ReadTranscript && st.TranscriptPath != "" {
			limit := int64(maxTranscriptRead)
			if events[len(events)-1].EventType == schema.SessionEnd {
				limit = maxFinalRead
			}
			recs, next, err := transcript.ReadFrom(st.TranscriptPath, st.Offset, limit)
			if err == nil {
				var replyInTranscript bool
				recovered, replyInTranscript = st.recover(recs, c.Base, c.Reply)
				st.Offset = next
				if replyInTranscript {
					events = dropReply(events)
				} else if c.Reply != "" {
					st.PendingReplies = remember(st.PendingReplies, hash(c.Reply))
				}
			} else if c.Reply != "" {
				st.PendingReplies = remember(st.PendingReplies, hash(c.Reply))
			}
			// A transcript read error must not lose the hook's own events.
		}

		for i := range events {
			if events[i].EventType == schema.AssistantMessage && events[i].Model == "" {
				events[i].Model = st.Model // the Stop reply: written by the latest model seen
			}
			if events[i].AgentVersion == "" {
				events[i].AgentVersion = st.Version
			}
		}
		all := append(recovered, events...)
		st.highlight(all, dataDir)
		if err := w.Append(all); err != nil {
			return err
		}
		return save(statePath(dataDir, session), st)
	})
}

// highlight runs the rules over events about to be written, so tool
// calls recovered from the transcript are tagged like hooked ones. A rule
// failure costs highlights, never the events.
func (st *State) highlight(events []schema.Event, dataDir string) {
	for _, e := range events {
		if st.Workspace == "" && e.Cwd != "" {
			st.Workspace = e.Cwd
		}
	}
	ctx := Context(st.Workspace, dataDir)
	ctx.Mode = st.Mode
	for i := range events {
		if !rules.Evaluates(events[i]) {
			continue
		}
		hs, err := rules.Evaluate(events[i], &ctx)
		if err != nil {
			Warn("highlight rules: " + err.Error())
			if hs == nil {
				continue // the rules did not run: leave the event unstamped
			}
		}
		events[i].Highlights = hs
		events[i].RulesVersion = rules.Version
	}
	st.Mode = ctx.Mode
}

// Context builds the rules context for a session on this machine.
func Context(workspace, dataDir string) rules.Context {
	ctx := rules.Context{Workspace: workspace, DataDir: dataDir}
	ctx.Home, _ = os.UserHomeDir()
	for _, v := range []string{os.TempDir(), os.Getenv("TMPDIR"), os.Getenv("TEMP"), os.Getenv("TMP")} {
		if v != "" && !slices.Contains(ctx.TempDirs, v) {
			ctx.TempDirs = append(ctx.TempDirs, v)
		}
	}
	return ctx
}

// recover turns transcript records into the events hooks did not supply.
// It reports whether the Stop reply text was found, in which case the
// transcript copy (with its real timestamp) replaces the hook's.
func (st *State) recover(recs []transcript.Record, base schema.Event, reply string) ([]schema.Event, bool) {
	var out []schema.Event
	found := false
	for _, r := range recs {
		if r.PromptID != "" {
			st.PromptID = r.PromptID
		}
		if r.Model != "" {
			st.Model = r.Model
		}
		prevVersion := st.Version
		if r.Version != "" {
			st.Version = r.Version
		}
		if st.seen[r.UUID] && r.UUID != "" {
			continue // already in the log (state was rebuilt)
		}
		e := base
		e.EventID = schema.NewEventID(r.TS)
		e.TS = r.TS.UTC()
		e.Origin = schema.OriginTranscript
		e.TranscriptUUID = r.UUID
		e.TurnID, e.PromptRef = st.PromptID, ""
		if st.PromptID != "" {
			e.PromptRef = e.Session + "#" + st.PromptID
		}
		e.AgentVersion = st.Version

		if prevVersion != "" && r.Version != "" && r.Version != prevVersion {
			// The harness updated itself mid-session (seen: Claude Code
			// 2.1.295 to 2.1.296 between resumed runs).
			v := e
			v.EventID = schema.NewEventID(r.TS)
			v.TranscriptUUID = ""
			v.EventType = schema.HarnessNotice
			v.Notice = &schema.Notice{Kind: "agent_version_change", Level: "notice"}
			v.Content = text(fmt.Sprintf("%s version changed from %s to %s", base.Agent, prevVersion, r.Version))
			out = append(out, v)
		}

		switch r.Kind {
		case transcript.Notice:
			e.EventType = schema.HarnessNotice
			e.Content = text(r.Text)
			if n := r.Notice; n != nil {
				e.Notice = &schema.Notice{
					Kind: n.Subtype, Level: n.Level, Category: n.Category, Explanation: n.Explanation,
					OriginalModel: n.OriginalModel, FallbackModel: n.FallbackModel, Trigger: n.Trigger, Scope: n.Scope,
				}
			}
		case transcript.Skipped:
			e.EventType = schema.AfrError
			e.Content = text(fmt.Sprintf("transcript line of %d bytes skipped: larger than one read", r.Bytes))
		case transcript.Prompt:
			// A message the harness wrote itself (isMeta) can reuse the
			// prompt's promptId, so it is never matched against hook prompts.
			if !r.Meta && slices.Contains(st.HookPromptIDs, r.PromptID) {
				continue
			}
			// A message the harness injected (such as a background task
			// notification) that never went through the prompt hook.
			e.EventType = schema.PromptSubmit
			e.Content = text(r.Text)
		case transcript.Text:
			if i := slices.Index(st.PendingReplies, hash(r.Text)); i >= 0 {
				st.PendingReplies = slices.Delete(st.PendingReplies, i, i+1) // already logged from Stop
				continue
			}
			if reply != "" && !found && r.Text == reply {
				found = true
			}
			e.EventType = schema.AssistantMessage
			e.Model = r.Model
			e.Content = text(r.Text)
		case transcript.ToolUse:
			if i := slices.Index(st.HookToolIDs, r.ToolID); i >= 0 {
				st.HookToolIDs = slices.Delete(st.HookToolIDs, i, i+1)
				continue
			}
			// The harness handled this call without running any hook.
			e.EventType = schema.ToolCall
			e.Tool, e.ToolCallID = r.ToolName, r.ToolID
			in, n, cut := schema.CapJSON(r.Input)
			e.Content = &schema.Content{Input: in, Truncated: cut, Bytes: n}
			st.TranscriptToolIDs = remember(st.TranscriptToolIDs, r.ToolID)
		case transcript.ToolResult:
			i := slices.Index(st.TranscriptToolIDs, r.ToolID)
			if i < 0 {
				continue // a hook recorded this result
			}
			st.TranscriptToolIDs = slices.Delete(st.TranscriptToolIDs, i, i+1)
			e.EventType = schema.ToolResult
			e.ToolCallID, e.ToolError = r.ToolID, r.IsError
			out2, n, cut := schema.CapJSON(r.Output)
			e.Content = &schema.Content{Output: out2, Truncated: cut, Bytes: n}
		default:
			continue
		}
		out = append(out, e)
	}
	return out, found
}

// dropReply removes the Stop reply event, keeping the turn end.
func dropReply(events []schema.Event) []schema.Event {
	return slices.DeleteFunc(slices.Clone(events), func(e schema.Event) bool {
		return e.EventType == schema.AssistantMessage && e.Origin == ""
	})
}

func text(s string) *schema.Content {
	t, n, cut := schema.CapText(s)
	return &schema.Content{Text: t, Truncated: cut, Bytes: n}
}

func remember(ids []string, id string) []string {
	if id == "" || slices.Contains(ids, id) {
		return ids
	}
	ids = append(ids, id)
	if len(ids) > maxIDs {
		ids = ids[len(ids)-maxIDs:]
	}
	return ids
}

func hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// load reads the state file. ok is false if it is missing or damaged.
func load(path string) (State, bool) {
	var st State
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &st) != nil {
		return State{}, false
	}
	return st, true
}

func transcriptShrank(st State) bool {
	if st.TranscriptPath == "" {
		return false
	}
	info, err := os.Stat(st.TranscriptPath)
	return err == nil && info.Size() < st.Offset
}

// seedFromLog rebuilds the id lists from events already logged, and
// resets the transcript offset so the read that follows recovers only
// what is genuinely missing.
func (st *State) seedFromLog(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st.Offset = 0
	st.HookToolIDs, st.HookPromptIDs, st.TranscriptToolIDs, st.PendingReplies = nil, nil, nil, nil
	st.seen = map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var e schema.Event
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		if e.TranscriptUUID != "" {
			st.seen[e.TranscriptUUID] = true
		}
		switch e.EventType {
		case schema.ToolCall:
			// Logged by a hook or already recovered: either way, skip it
			// and its result when the transcript shows them again.
			st.HookToolIDs = remember(st.HookToolIDs, e.ToolCallID)
		case schema.PromptSubmit:
			st.HookPromptIDs = remember(st.HookPromptIDs, e.TurnID)
		case schema.AssistantMessage:
			// Replies recovered from the transcript are skipped by uuid;
			// only Stop replies (no uuid) need matching by text.
			if e.Content != nil && e.Origin == "" {
				st.PendingReplies = append(st.PendingReplies, hash(e.Content.Text))
			}
		}
	}
	if len(st.PendingReplies) > maxIDs {
		st.PendingReplies = st.PendingReplies[len(st.PendingReplies)-maxIDs:]
	}
	return sc.Err()
}

// save writes the state atomically: a temp file renamed over the old one.
func save(path string, st State) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp")
	_ = os.Remove(tmp)
	if err := platform.WriteFilePrivate(tmp, b); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
