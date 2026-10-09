package schema

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func sample() Event {
	return Event{
		Schema: Version, EventID: "id1", TS: time.Date(2026, 10, 9, 17, 5, 52, 0, time.UTC),
		Session: "s1", TurnID: "p1", User: "u", Host: "h", Agent: "claude-code",
		EventType: ToolCall, Tool: "Bash", ToolCallID: "t1",
		Content: &Content{Input: json.RawMessage(`{"command":"ls"}`)},
	}
}

func TestSealRoundTrip(t *testing.T) {
	e := sample()
	if err := e.Seal(7, "prev"); err != nil {
		t.Fatal(err)
	}
	if e.Seq != 7 || e.PrevHash != "prev" || e.Hash == "" || e.ContentHash == "" {
		t.Fatalf("Seal left fields unset: %+v", e)
	}
	line, _ := json.Marshal(e)
	var back Event
	if err := json.Unmarshal(line, &back); err != nil {
		t.Fatal(err)
	}
	h, err := back.ComputeHash()
	if err != nil || h != e.Hash {
		t.Fatalf("hash after round trip = %q, want %q (%v)", h, e.Hash, err)
	}
}

func TestHashIgnoresPurgedContent(t *testing.T) {
	e := sample()
	_ = e.Seal(1, "")
	purged := e
	purged.Content = nil // retention purge keeps content_hash
	if h, _ := purged.ComputeHash(); h != e.Hash {
		t.Fatal("purging content changed the event hash")
	}
	tampered := e
	tampered.Tool = "Write"
	if h, _ := tampered.ComputeHash(); h == e.Hash {
		t.Fatal("changing a field did not change the hash")
	}
}

func TestCaps(t *testing.T) {
	small := `{"a":1}`
	if out, _, cut := CapJSON(json.RawMessage(small)); cut || string(out) != small {
		t.Fatal("small JSON was altered")
	}
	big := `"` + strings.Repeat("é", MaxContentBytes) + `"`
	out, n, cut := CapJSON(json.RawMessage(big))
	if !cut || n != len(big) || !json.Valid(out) {
		t.Fatalf("big JSON: cut=%v n=%d valid=%v", cut, n, json.Valid(out))
	}
	s, _, cut := CapText(strings.Repeat("é", MaxContentBytes))
	if !cut || len(s) > MaxContentBytes || !strings.HasSuffix(s, "é") {
		t.Fatal("CapText did not cut on a rune boundary")
	}
}
