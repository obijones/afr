package coverage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/obijones/afr/internal/schema"
)

var t0 = time.Date(2026, 10, 12, 17, 0, 0, 0, time.UTC)

// line builds one synthetic transcript line.
func line(sec int, fields map[string]any) string {
	fields["timestamp"] = t0.Add(time.Duration(sec) * time.Second).Format(time.RFC3339Nano)
	fields["uuid"] = "u" + fields["timestamp"].(string)
	b, _ := json.Marshal(fields)
	return string(b)
}

func user(sec int, prompt, text string, meta bool) string {
	return line(sec, map[string]any{"type": "user", "promptId": prompt, "isMeta": meta, "cwd": `C:\work`, "entrypoint": "claude-desktop",
		"message": map[string]any{"role": "user", "content": text}})
}

func stop(sec int, commands ...string) string {
	var infos []map[string]any
	for _, c := range commands {
		infos = append(infos, map[string]any{"command": c})
	}
	return line(sec, map[string]any{"type": "system", "subtype": "stop_hook_summary", "level": "suggestion", "hookInfos": infos})
}

func write(t *testing.T, dir, project, session string, lines ...string) string {
	t.Helper()
	p := filepath.Join(dir, project, session+".jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func submit(id string, origin string) schema.Event {
	return schema.Event{EventType: schema.PromptSubmit, TurnID: id, Origin: origin}
}

func TestCompare(t *testing.T) {
	dir := t.TempDir()
	afr := `C:\Users\u\afr\afr.exe hook`
	p := write(t, dir, "proj", "s1",
		user(0, "p1", "first, recorded live", false),
		stop(5, afr, "callback"),
		user(10, "p1", "a second message reusing the prompt id", false),
		user(20, "p2", "<system-reminder>the app's own note</system-reminder>", true),
		user(30, "p3", "<agent-message from=\"a1\">report</agent-message>", false),
		user(40, "p4", "<command-name>/model</command-name>", false),
		user(50, "p5", "typed while afr's hooks were removed", false),
		stop(55, "callback"),
		user(60, "p6", "recovered later from the transcript", false),
	)
	r, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Session != "s1" || r.App != "claude-desktop" || r.Cwd != `C:\work` || r.Prompts != 4 || r.Harness != 1 || r.Stops != 2 {
		t.Fatalf("read: %+v", r)
	}

	got := Compare(r, []schema.Event{submit("p1", ""), submit("p6", schema.OriginTranscript)})
	if got.Status != Partial || got.Hooked != 1 || got.Recovered != 1 || len(got.Missing) != 2 || got.TypedMissing() != 1 {
		t.Errorf("partial: %+v", got)
	}
	if len(got.StopsNoAfr) != 1 || !got.StopsNoAfr[0].Equal(t0.Add(55*time.Second)) {
		t.Errorf("stops without afr: %v", got.StopsNoAfr)
	}
	if !got.Missing[1].TS.Equal(t0.Add(50*time.Second)) || got.Missing[1].Harness || !got.Missing[0].Harness {
		t.Errorf("missing: %+v", got.Missing)
	}

	if m := Compare(r, nil); m.Status != Missed || m.Logged || len(m.Missing) != 4 {
		t.Errorf("missed: %+v", m)
	}
	all := []schema.Event{submit("p1", ""), submit("p3", ""), submit("p5", schema.OriginTranscript), submit("p6", "")}
	if l := Compare(r, all); l.Status != Late || len(l.Missing) != 0 {
		t.Errorf("late: %+v", l)
	}
}

// Harness messages never pass through the prompt hook, so afr recovering
// them from the transcript is normal: the session is still complete.
func TestRecoveredHarnessMessageIsComplete(t *testing.T) {
	p := write(t, t.TempDir(), "proj", "s2",
		user(0, "p1", "count the files", false),
		user(9, "p2", "<task-notification>\n<task-id>a1</task-id>", false),
		stop(10, "/usr/local/bin/afr hook"),
	)
	r, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}
	if c := Compare(r, []schema.Event{submit("p1", ""), submit("p2", schema.OriginTranscript)}); c.Status != Complete {
		t.Errorf("status %q: %+v", c.Status, c)
	}
}

func TestIsAfrHook(t *testing.T) {
	for cmd, want := range map[string]bool{
		`C:\Users\u\afr\afr.exe hook`:           true,
		`"C:\Program Files\afr\afr.exe" hook`:   true,
		`/usr/local/bin/afr hook`:               true,
		`afr hook`:                              true,
		`AFR.EXE HOOK`:                          true,
		`callback`:                              false,
		`/opt/safr hook`:                        false,
		`C:\tools\afr.exe report`:               false,
		`node C:\hooks\afr-notify.js hook`:      false,
		`C:\Users\u\afr\afr.exe hookshot --now`: false,
	} {
		if got := IsAfrHook(cmd); got != want {
			t.Errorf("IsAfrHook(%q) = %v, want %v", cmd, got, want)
		}
	}
}

func TestTranscripts(t *testing.T) {
	dir := t.TempDir()
	old := write(t, dir, "a", "old", user(0, "p", "x", false))
	cur := write(t, dir, "b", "new", user(0, "p", "x", false))
	write(t, dir, filepath.Join("b", "new", "subagents"), "agent-1", user(0, "p", "x", false))
	week := time.Now().AddDate(0, 0, -8)
	if err := os.Chtimes(old, week, week); err != nil {
		t.Fatal(err)
	}
	got, err := Transcripts(dir, time.Now().AddDate(0, 0, -7))
	if err != nil || len(got) != 1 || got[0] != cur {
		t.Errorf("transcripts %v, %v", got, err)
	}
}

func TestProjectsDir(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "proj", "s1", user(0, "p", "x", false))
	if got := ProjectsDir([]string{"", filepath.Join(dir, "gone", "x.jsonl"), p}); got != dir {
		t.Errorf("ProjectsDir = %q, want %q", got, dir)
	}
}
