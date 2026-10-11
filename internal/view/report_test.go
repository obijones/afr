package view

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/obijones/afr/internal/schema"
)

func reportOf(t *testing.T, sessions []ReportSession) string {
	t.Helper()
	var b bytes.Buffer
	o := ReportOptions{Version: "test", Generated: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC), Loc: time.UTC}
	if err := Report(&b, sessions, o); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func dataOf(t *testing.T, page string) map[string]any {
	t.Helper()
	m := regexp.MustCompile(`(?s)<script type="application/json" id="afr-data">(.*?)</script>`).FindStringSubmatch(page)
	if m == nil {
		t.Fatal("no data block")
	}
	var d map[string]any
	if err := json.Unmarshal([]byte(m[1]), &d); err != nil {
		t.Fatal(err)
	}
	return d
}

// Captured text must never become markup: it stays inside the JSON data
// block, escaped, and the page has only the two script tags afr wrote.
func TestReportCannotBeInjected(t *testing.T) {
	evil := "</script><script>alert(1)</script><img src=x onerror=alert(2)> ‮gnp.exe  "
	evs := synthetic()
	evs[1].Content = &schema.Content{Text: evil}
	evs[5].Content = &schema.Content{Output: json.RawMessage(`{"stdout":` + jsonString(evil) + `}`)}
	for i := range evs {
		evs[i].Cwd, evs[i].User, evs[i].Host = evil, evil, evil
		if evs[i].Model != "" {
			evs[i].Model = evil
		}
		if evs[i].EventType == schema.SessionEnd {
			evs[i].EndReason = evil
		}
	}
	page := reportOf(t, []ReportSession{{Events: evs, Integrity: evil, OK: false}})

	if n := strings.Count(strings.ToLower(page), "<script"); n != 2 {
		t.Errorf("page has %d <script tags, want 2", n)
	}
	for _, bad := range []string{"<img", "alert(1)</script>", "‮", " "} {
		if strings.Contains(page, bad) {
			t.Errorf("page contains %q outside escaped data", bad)
		}
	}
	// The text survives as data, with the bidi override made visible.
	esc := strings.NewReplacer("<", `\`+"u003c", ">", `\`+"u003e")
	if !strings.Contains(page, esc.Replace("</script><script>alert(1)")) || !strings.Contains(page, esc.Replace("<U+202E>")) {
		t.Error("captured text missing from the data block")
	}
}

func jsonString(s string) string { b, _ := json.Marshal(s); return string(b) }

// The CSP allows exactly the inline style and script afr ships.
func TestReportCSPMatchesInlineCode(t *testing.T) {
	page := reportOf(t, []ReportSession{{Events: synthetic(), Integrity: "ok", OK: true}})
	csp := regexp.MustCompile(`http-equiv="Content-Security-Policy" content="([^"]*)"`).FindStringSubmatch(page)
	if csp == nil || !strings.Contains(csp[1], "default-src 'none'") {
		t.Fatalf("CSP missing or not default-deny: %v", csp)
	}
	hash := func(s string) string {
		h := sha256.Sum256([]byte(s))
		return "'sha256-" + base64.StdEncoding.EncodeToString(h[:]) + "'"
	}
	style := regexp.MustCompile(`(?s)<style>(.*?)</style>`).FindStringSubmatch(page)
	script := regexp.MustCompile(`(?s)<script>(.*?)</script>`).FindStringSubmatch(page)
	if style == nil || script == nil {
		t.Fatal("inline style or script missing")
	}
	if !strings.Contains(csp[1], "style-src "+hash(style[1])) || !strings.Contains(csp[1], "script-src "+hash(script[1])) {
		t.Error("CSP hashes do not match the inline style and script")
	}
	shell := regexp.MustCompile(`(?s)<script type="application/json" id="afr-data">.*?</script>`).ReplaceAllString(page, "")
	if strings.Contains(shell, "http://") || strings.Contains(shell, "https://") {
		t.Error("page references a network resource")
	}
}

// The report shows the same lines as the replay, from the same model.
func TestReportMatchesReplayModel(t *testing.T) {
	evs := synthetic()
	m, err := Build(evs, nil)
	if err != nil {
		t.Fatal(err)
	}
	d := dataOf(t, reportOf(t, []ReportSession{{Events: evs, Integrity: "ok", OK: true}}))
	s := d["sessions"].([]any)[0].(map[string]any)
	lines := s["lines"].([]any)
	if len(lines) != len(m.Lines) {
		t.Fatalf("report has %d lines, model %d", len(lines), len(m.Lines))
	}
	for i, l := range lines {
		lm := l.(map[string]any)
		if lm["kind"] != m.Lines[i].Kind {
			t.Errorf("line %d kind %v, model %s", i, lm["kind"], m.Lines[i].Kind)
		}
	}
	var sawHigh, sawBlocked bool
	for _, l := range lines {
		lm := l.(map[string]any)
		if marks, ok := lm["marks"].([]any); ok && marks[0].(map[string]any)["sev"] == "high" {
			sawHigh = true
		}
		if tl, ok := lm["tool"].(map[string]any); ok && tl["failed"] == true {
			sawBlocked = true
		}
	}
	if !sawHigh || !sawBlocked {
		t.Errorf("highlight or failed call missing: high=%v failed=%v", sawHigh, sawBlocked)
	}
	if s["failed"].(float64) != 1 || s["integrity_ok"] != true {
		t.Errorf("session counts: failed=%v integrity_ok=%v", s["failed"], s["integrity_ok"])
	}
}

func TestReportMasksSecretsInSummaries(t *testing.T) {
	evs := synthetic()
	evs[1].Content = &schema.Content{Text: "use ghp_abcdefghijklmnopqrstuvwxyz0123456789 please"}
	d := dataOf(t, reportOf(t, []ReportSession{{Events: evs, Integrity: "ok", OK: true}}))
	for _, l := range d["sessions"].([]any)[0].(map[string]any)["lines"].([]any) {
		lm := l.(map[string]any)
		if lm["kind"] == "PROMPT" && strings.Contains(lm["text"].(string), "ghp_") {
			if strings.Contains(lm["text"].(string), "0123456789") {
				t.Errorf("secret not masked in summary: %v", lm["text"])
			}
			return
		}
	}
	t.Error("prompt line not found")
}
