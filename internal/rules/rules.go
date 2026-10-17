// Package rules tags recorded events with highlights: short, ranked
// pointers that tell a reviewer where to look first. Rules never block,
// delay, or change anything; a rule that fails costs highlights, never
// the event.
//
// Rules match command text and file paths, so obfuscated commands can
// evade them. They aid review; they are not detection guarantees. The
// framework grounding and severity choices are in docs/highlight-rules.md.
package rules

import (
	"fmt"
	"slices"
	"strings"

	"github.com/obijones/afr/internal/schema"
)

// Version is stamped on every event the rules evaluate at write time.
// Bump it whenever a rule's matching or severity changes, so a replay can
// tell stored highlights from what the current rules would say.
const Version = "4"

// Severities, highest first. Severity means review priority.
const (
	High   = "high"
	Medium = "medium"
	Low    = "low"
)

// Rank orders severities: higher is more urgent. Unknown values rank 0.
func Rank(severity string) int {
	switch severity {
	case High:
		return 3
	case Medium:
		return 2
	case Low:
		return 1
	}
	return 0
}

// Rule ids. Rules 1 to 12 come from docs/threat-model.md; 13 to 15 were
// added with the framework research; 16 from the desktop app testing.
const (
	CredentialFile    = "secrets.read_credential_file" // 1
	SecretInContent   = "secrets.in_content"           // 2
	DownloadPipeShell = "exec.download_pipe_shell"     // 3
	DecodeAndRun      = "exec.decode_and_run"          // 4
	OutboundTransfer  = "net.outbound_transfer"        // 5
	WriteOutside      = "fs.write_outside_workspace"   // 6
	Autostart         = "persist.autostart"            // 7
	Destructive       = "fs.destructive_command"       // 8
	HistoryRewrite    = "git.history_rewrite"          // 9
	PrivEscalation    = "priv.escalation"              // 10
	MonitoringTamper  = "agent.monitoring_tamper"      // 11
	PermissionBypass  = "agent.permission_bypass"      // 12
	EnvDump           = "secrets.env_dump"             // 13
	ReverseShell      = "net.reverse_shell_or_tunnel"  // 14
	InstallFromURL    = "exec.install_from_url"        // 15
	ScriptBypass      = "exec.script_policy_bypass"    // 16
)

// Info describes one rule for documentation and tests.
type Info struct {
	ID    string
	Title string
	Tiers string // which matches get which severity
}

// Catalog lists every rule in catalog order.
var Catalog = []Info{
	{CredentialFile, "Credential file read", "high: host credentials (SSH keys, cloud, kube, keychains, password stores); medium: project .env files"},
	{SecretInContent, "Secret-shaped string in content", "medium"},
	{DownloadPipeShell, "Download piped to a shell", "high"},
	{DecodeAndRun, "Decode and run", "high: decoded content fed to an interpreter; medium: bare encoded command"},
	{OutboundTransfer, "Outbound data transfer", "high: to file-sharing, paste, or webhook destinations; medium: other uploads"},
	{WriteOutside, "Write outside the workspace", "medium; low: temp folders; caches and build output are not flagged"},
	{Autostart, "Persistence or autostart", "high"},
	{Destructive, "Destructive command", "high: root, home, system paths, block devices, cloud resources; medium: elsewhere"},
	{HistoryRewrite, "Git history rewrite", "medium"},
	{PrivEscalation, "Privilege escalation", "high: setuid/setgid, setcap, sudoers, admin groups; medium: sudo/su/runas, chmod 777, chown"},
	{MonitoringTamper, "Agent monitoring tamper", "high"},
	{PermissionBypass, "Agent permission bypass", "high: the agent edits its permissions or launches agents with bypass flags; medium: a session starts in bypass mode"},
	{EnvDump, "Environment variable dump", "medium"},
	{ReverseShell, "Reverse shell or tunnel", "high: reverse shells, ngrok-class tunnels; medium: cloudflared, VS Code and dev tunnels, ssh -R"},
	{InstallFromURL, "Package install from a URL", "medium"},
	{ScriptBypass, "Script run with policy bypass or from a temp folder", "medium"},
}

// Context is what rules need beyond the event. Paths may be in Linux or
// Windows form whatever the OS afr runs on, since a replay can read a log
// recorded elsewhere.
type Context struct {
	Workspace  string   // project root; writes under it are not "outside"
	Workspaces []string // more folders that count as the workspace (see Workspace.Apply)
	Home       string   // the user's home directory
	DataDir    string   // afr's data directory
	TempDirs   []string // extra temp folders ($TMPDIR, %TEMP%)

	// Custom holds customer rules and overrides of built-in rules; nil
	// means built-in rules only.
	Custom *Set

	// Mode is the last permission mode seen in the session. Evaluate
	// updates it, so the caller keeps it between events.
	Mode string
}

// Evaluate returns the highlights for one event, highest severity first,
// one per rule. It never panics: a failing rule is reported in err and
// the other rules still run.
func Evaluate(e schema.Event, ctx *Context) (hs []schema.Highlight, err error) {
	var errs []string
	set := highlightSet{}
	run := func(name string, f func()) {
		defer func() {
			if r := recover(); r != nil {
				errs = append(errs, fmt.Sprintf("rule %s: panic: %v", name, r))
			}
		}()
		f()
	}

	if e.PermissionMode != "" && ctx != nil {
		run("permission mode", func() {
			if e.PermissionMode == "bypassPermissions" && ctx.Mode != "bypassPermissions" {
				set.add(PermissionBypass, Medium, "session in bypassPermissions mode")
			}
		})
		ctx.Mode = e.PermissionMode
	}

	c := ctx
	if c == nil {
		c = &Context{}
	}
	var in *input
	if e.Content != nil {
		switch e.EventType {
		case schema.ToolCall:
			parsed := parseInput(e.Tool, e.Content.Input, e.Cwd, c)
			in = &parsed
			for _, r := range toolRules {
				run(r.name, func() { r.f(parsed, c, set) })
			}
			run("secrets", func() { secretsIn(string(e.Content.Input), set) })
		case schema.ToolResult:
			run("secrets", func() { secretsIn(string(e.Content.Output), set) })
		case schema.PromptSubmit, schema.AssistantMessage, schema.SubagentEnd:
			run("secrets", func() { secretsIn(e.Content.Text, set) })
		}
	}
	if cs := c.Custom; cs != nil {
		for id, h := range set {
			if o, ok := cs.Overrides[id]; ok {
				if o.Disabled {
					delete(set, id)
				} else {
					h.Severity = o.Severity
					set[id] = h
				}
			}
		}
		if e.Content != nil {
			for i := range cs.Rules {
				r := &cs.Rules[i]
				run(r.ID, func() { r.evaluate(e, in, set) })
			}
		}
	}
	if len(errs) > 0 {
		err = fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return set.list(), err
}

// Evaluates reports whether the rules look at this kind of event at all.
// Events of other kinds are not stamped with a rules version.
func Evaluates(e schema.Event) bool {
	switch e.EventType {
	case schema.ToolCall, schema.ToolResult, schema.PromptSubmit, schema.AssistantMessage,
		schema.SubagentEnd, schema.SessionStart:
		return true
	}
	return false
}

// highlightSet keeps the highest severity per rule.
type highlightSet map[string]schema.Highlight

func (s highlightSet) add(id, severity, detail string) {
	if old, ok := s[id]; ok && Rank(old.Severity) >= Rank(severity) {
		return
	}
	s[id] = schema.Highlight{RuleID: id, Severity: severity, Detail: clip(detail, 120)}
}

func (s highlightSet) list() []schema.Highlight {
	if len(s) == 0 {
		return nil
	}
	out := make([]schema.Highlight, 0, len(s))
	for _, h := range s {
		out = append(out, h)
	}
	order := map[string]int{}
	for i, r := range Catalog {
		order[r.ID] = i
	}
	slices.SortFunc(out, func(a, b schema.Highlight) int {
		if d := Rank(b.Severity) - Rank(a.Severity); d != 0 {
			return d
		}
		ia, oka := order[a.RuleID]
		ib, okb := order[b.RuleID]
		switch {
		case !oka && !okb:
			return strings.Compare(a.RuleID, b.RuleID)
		case !oka:
			return 1 // customer rules after built-ins of the same severity
		case !okb:
			return -1
		}
		return ia - ib
	})
	return out
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n] + "..."
}
