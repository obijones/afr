package rules

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/obijones/afr/internal/schema"
)

func writeRules(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const acme = `
rules:
  - id: acme.prod_db
    title: Production database
    severity: high
    match:
      command: '(?i)\bpsql\b.*prod-db'
  - id: acme.customer_data
    severity: medium
    match:
      read_path: ['/data/customers/**', '**/exports/*.csv']
  - id: acme.internal_hostname
    severity: low
    match:
      content: 'corp\.acme\.internal'
  - id: acme.bash_writes_config
    severity: medium
    match:
      tool: '^Bash$'
      write_path: '**/config/*.yaml'
overrides:
  secrets.env_dump: {disabled: true}
  fs.write_outside_workspace: {severity: high}
`

func TestCustomRules(t *testing.T) {
	dir := t.TempDir()
	set, problems := LoadFiles(dir, []string{writeRules(t, dir, "acme.yaml", acme)})
	if len(problems) > 0 || set == nil {
		t.Fatalf("problems: %v", problems)
	}
	if !strings.HasPrefix(set.Version(), Version+"+") || len(set.Version()) != len(Version)+9 {
		t.Fatalf("version = %q", set.Version())
	}
	ctx := linuxCtx
	ctx.Custom = set
	cases := []struct {
		name string
		e    schema.Event
		want map[string]string
	}{
		{"command", bash("psql -h prod-db.acme.internal -c 'select 1'"), map[string]string{"acme.prod_db": High}},
		{"read glob deep", call("Read", map[string]any{"file_path": "/data/customers/eu/2026/list.txt"}), map[string]string{"acme.customer_data": Medium}},
		{"read glob any depth", call("Read", map[string]any{"file_path": "/home/u/proj/exports/jan.csv"}), map[string]string{"acme.customer_data": Medium}},
		{"read glob no match", call("Read", map[string]any{"file_path": "/home/u/proj/exports/sub/jan.csv"}), nil},
		{"content in prompt", schema.Event{EventType: schema.PromptSubmit, Content: &schema.Content{Text: "ssh to db1.corp.acme.internal"}}, map[string]string{"acme.internal_hostname": Low}},
		{"tool and path both", bash("cp a.yaml /home/u/proj/config/app.yaml"), map[string]string{"acme.bash_writes_config": Medium}},
		{"tool mismatch", call("Write", map[string]any{"file_path": "/home/u/proj/config/app.yaml", "content": "x"}), nil},
		{"disabled built-in", bash("env"), nil},
		{"re-severity built-in", bash("echo x > /srv/a.txt"), map[string]string{WriteOutside: High}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := ctx
			hs, err := Evaluate(tc.e, &c)
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]string{}
			for _, h := range hs {
				got[h.RuleID] = h.Severity
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", hs, tc.want)
			}
			for id, sev := range tc.want {
				if got[id] != sev {
					t.Errorf("%s = %q, want %q", id, got[id], sev)
				}
			}
		})
	}
}

func TestCustomRuleProblems(t *testing.T) {
	dir := t.TempDir()
	bad := `
rules:
  - id: NoDot
    severity: high
    match: {command: x}
  - id: fs.destructive_command
    severity: high
    match: {command: x}
  - id: acme.bad_severity
    severity: critical
    match: {command: x}
  - id: acme.bad_regex
    severity: high
    match: {command: '(unclosed'}
  - id: acme.no_match
    severity: high
  - id: acme.good
    severity: low
    match: {command: 'ok'}
  - id: acme.good
    severity: low
    match: {command: 'again'}
overrides:
  acme.unknown: {disabled: true}
  priv.escalation: {severity: urgent}
`
	set, problems := LoadFiles(dir, []string{
		writeRules(t, dir, "bad.yaml", bad),
		writeRules(t, dir, "typo.yaml", "rules:\n  - id: acme.typo\n    severty: high\n    match: {command: x}\n"),
	})
	if set == nil || len(set.Rules) != 1 || set.Rules[0].ID != "acme.good" {
		t.Fatalf("good rule not kept: %+v", set)
	}
	for _, want := range []string{"org.rule_name", "is a built-in rule", "severity must be", "unclosed", "at least one of", "defined twice", "no built-in rule", "severty"} {
		found := false
		for _, p := range problems {
			found = found || strings.Contains(p.Error(), want)
		}
		if !found {
			t.Errorf("no problem mentions %q: %v", want, problems)
		}
	}
	if len(set.Overrides) != 0 {
		t.Errorf("bad overrides kept: %v", set.Overrides)
	}
}

func TestManagedRulesReplacePersonal(t *testing.T) {
	root := t.TempDir()
	managed, personal := filepath.Join(root, "managed"), filepath.Join(root, "personal")
	writeRules(t, personal, "mine.yaml", "overrides:\n  agent.monitoring_tamper: {disabled: true}\n")
	set, _ := LoadDirs(managed, personal)
	if set == nil || set.Dir != personal {
		t.Fatalf("personal rules not used when no managed rules: %+v", set)
	}
	writeRules(t, managed, "org.yaml", "rules:\n  - id: org.x\n    severity: low\n    match: {command: x}\n")
	set, _ = LoadDirs(managed, personal)
	if set == nil || set.Dir != managed || len(set.Overrides) != 0 {
		t.Fatalf("managed rules should replace personal ones: %+v", set)
	}
	before := set.Version()
	writeRules(t, managed, "org.yaml", "rules:\n  - id: org.x\n    severity: high\n    match: {command: x}\n")
	if set, _ = LoadDirs(managed, personal); set.Version() == before {
		t.Fatal("editing a rule file did not change the rules version")
	}
}

func TestOversizeRuleFile(t *testing.T) {
	dir := t.TempDir()
	p := writeRules(t, dir, "big.yaml", "# "+strings.Repeat("x", maxRuleFileSize))
	if set, problems := LoadFiles(dir, []string{p}); set != nil || len(problems) != 1 {
		t.Fatalf("got %v, %v", set, problems)
	}
}

func TestGlobRegexp(t *testing.T) {
	cases := []struct {
		glob, path string
		want       bool
	}{
		{"/data/**", "/data/a/b.txt", true},
		{"/data/*", "/data/a/b.txt", false},
		{"*.pem", "/home/u/certs/server.pem", true},
		{"~/.kube/config", "/home/u/.kube/config", true},
		{`C:\Secrets\**`, "c:/secrets/x/y", true},
		{"**/build/**", "/w/build/out.bin", true},
		{"/etc/ssl/?.key", "/etc/ssl/a.key", true},
		{"/etc/ssl/?.key", "/etc/ssl/ab.key", false},
		{"report.csv", "/w/sub/report.csv", true},
		{"report.csv", "/w/sub/myreport.csv", false},
	}
	for _, tc := range cases {
		re := get2(t, globRegexp(tc.glob))
		if got := re.MatchString(tc.path); got != tc.want {
			t.Errorf("glob %q on %q = %v, want %v (%s)", tc.glob, tc.path, got, tc.want, globRegexp(tc.glob))
		}
	}
}

func get2(t *testing.T, p string) *regexp.Regexp {
	t.Helper()
	r, err := regexp.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCustomRuleOnResultOutput(t *testing.T) {
	dir := t.TempDir()
	set, _ := LoadFiles(dir, []string{writeRules(t, dir, "r.yaml", acme)})
	e := schema.Event{EventType: schema.ToolResult, Content: &schema.Content{Output: json.RawMessage(`{"stdout":"host db.corp.acme.internal"}`)}}
	hs, _ := Evaluate(e, &Context{Custom: set})
	if len(hs) != 1 || hs[0].RuleID != "acme.internal_hostname" {
		t.Fatalf("got %v", hs)
	}
}

func TestExampleRuleFileLoads(t *testing.T) {
	set, problems := LoadFiles("", []string{"../../docs/examples/custom-rules.yaml"})
	if len(problems) > 0 || set == nil || len(set.Rules) != 4 || len(set.Overrides) != 2 {
		t.Fatalf("example rules: %+v %v", set, problems)
	}
}
