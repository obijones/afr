package store

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/obijones/afr/internal/schema"
)

func ev(session, typ string) schema.Event {
	now := time.Now()
	return schema.Event{
		Schema: schema.Version, EventID: schema.NewEventID(now), TS: now.UTC(),
		Session: session, User: "u", Host: "h", Agent: "claude-code", EventType: typ,
		Content: &schema.Content{Text: "hello"},
	}
}

func readAll(t *testing.T, path string) []schema.Event {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []schema.Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var e schema.Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

func TestAppendChains(t *testing.T) {
	dir := t.TempDir()
	if err := Append(dir, []schema.Event{ev("s1", schema.SessionStart)}); err != nil {
		t.Fatal(err)
	}
	if err := Append(dir, []schema.Event{ev("s1", schema.AssistantMessage), ev("s1", schema.TurnEnd)}); err != nil {
		t.Fatal(err)
	}
	got := readAll(t, LogPath(dir, "s1"))
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3", len(got))
	}
	for i, e := range got {
		if e.Seq != int64(i+1) {
			t.Errorf("event %d seq = %d", i, e.Seq)
		}
		if i > 0 && e.PrevHash != got[i-1].Hash {
			t.Errorf("event %d does not chain to event %d", i, i-1)
		}
	}
	if probs, err := Verify(LogPath(dir, "s1")); err != nil || len(probs) != 0 {
		t.Fatalf("Verify: %v %v", probs, err)
	}
}

func TestAppendConcurrent(t *testing.T) {
	dir := t.TempDir()
	const n = 40
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := Append(dir, []schema.Event{ev("s1", schema.ToolCall)}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got := readAll(t, LogPath(dir, "s1")); len(got) != n {
		t.Fatalf("got %d events, want %d", len(got), n)
	}
	if probs, err := Verify(LogPath(dir, "s1")); err != nil || len(probs) != 0 {
		t.Fatalf("Verify after concurrent appends: %v %v", probs, err)
	}
}

func TestAppendLargeEvents(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		e := ev("s1", schema.ToolResult)
		e.Content = &schema.Content{Text: strings.Repeat("y", schema.MaxContentBytes)}
		if err := Append(dir, []schema.Event{e}); err != nil {
			t.Fatal(err)
		}
	}
	if probs, _ := Verify(LogPath(dir, "s1")); len(probs) != 0 {
		t.Fatalf("lines larger than the tail window broke the chain: %v", probs)
	}
}

func TestVerifyDetectsTampering(t *testing.T) {
	dir := t.TempDir()
	_ = Append(dir, []schema.Event{ev("s1", schema.SessionStart), ev("s1", schema.ToolCall), ev("s1", schema.SessionEnd)})
	path := LogPath(dir, "s1")
	b, _ := os.ReadFile(path)
	os.WriteFile(path, []byte(strings.Replace(string(b), `"tool.call"`, `"tool.result"`, 1)), 0o600)
	probs, err := Verify(path)
	if err != nil || len(probs) == 0 {
		t.Fatalf("tampering not detected: %v %v", probs, err)
	}
}

func TestPartialLastLineIgnored(t *testing.T) {
	dir := t.TempDir()
	_ = Append(dir, []schema.Event{ev("s1", schema.SessionStart)})
	f, _ := os.OpenFile(LogPath(dir, "s1"), os.O_WRONLY|os.O_APPEND, 0o600)
	f.WriteString(`{"seq":2,"hash":"trunc`) // crash mid-write, no newline
	f.Close()
	if err := Append(dir, []schema.Event{ev("s1", schema.SessionEnd)}); err != nil {
		t.Fatal(err)
	}
	probs, _ := Verify(LogPath(dir, "s1"))
	if len(probs) != 1 || !strings.Contains(probs[0].Detail, "unparseable") {
		t.Fatalf("want exactly the damaged line reported, got %v", probs)
	}
}

func TestMixedSessionsRejected(t *testing.T) {
	if err := Append(t.TempDir(), []schema.Event{ev("a", schema.ToolCall), ev("b", schema.ToolCall)}); err == nil {
		t.Fatal("mixed sessions accepted")
	}
}

func TestVerifyDetectsContentEdit(t *testing.T) {
	dir := t.TempDir()
	e := ev("s1", schema.PromptSubmit)
	e.Content = &schema.Content{Text: `delete <all> & "everything" café 日本`, Input: []byte(`{"a": "<b> & c", "n": 1}`)}
	if err := Append(dir, []schema.Event{e, ev("s1", schema.TurnEnd)}); err != nil {
		t.Fatal(err)
	}
	path := LogPath(dir, "s1")
	if probs, _ := Verify(path); len(probs) != 0 {
		t.Fatalf("clean log with escaped and non-ASCII content failed Verify: %v", probs)
	}
	b, _ := os.ReadFile(path)
	edited := strings.Replace(string(b), "everything", "nothing at", 1)
	if edited == string(b) {
		t.Fatal("test did not edit the content")
	}
	os.WriteFile(path, []byte(edited), 0o600)
	probs, _ := Verify(path)
	if len(probs) != 1 || !strings.Contains(probs[0].Detail, "content_hash") {
		t.Fatalf("content edit not detected: %v", probs)
	}
}

func TestVerifyAcceptsPurgedContent(t *testing.T) {
	dir := t.TempDir()
	_ = Append(dir, []schema.Event{ev("s1", schema.PromptSubmit)})
	path := LogPath(dir, "s1")
	b, _ := os.ReadFile(path)
	var e map[string]any
	json.Unmarshal(b, &e)
	delete(e, "content") // retention purge
	out, _ := json.Marshal(e)
	os.WriteFile(path, append(out, '\n'), 0o600)
	if probs, _ := Verify(path); len(probs) != 0 {
		t.Fatalf("purged content flagged as damage: %v", probs)
	}
}

func TestAppendLockTimeout(t *testing.T) {
	dir := t.TempDir()
	old := LockTimeout
	LockTimeout = 100 * time.Millisecond
	defer func() { LockTimeout = old }()
	_ = os.MkdirAll(SessionsDir(dir), 0o700)
	holder := flock.New(LogPath(dir, "s1") + ".lock")
	if err := holder.Lock(); err != nil {
		t.Fatal(err)
	}
	defer holder.Unlock()
	start := time.Now()
	err := Append(dir, []schema.Event{ev("s1", schema.ToolCall)})
	if err != ErrLockTimeout {
		t.Fatalf("err = %v, want ErrLockTimeout", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("Append waited %v", time.Since(start))
	}
}
