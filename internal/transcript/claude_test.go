package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixture = "../../testdata/claude-code/2.1.295/transcript.jsonl"

func count(recs []Record) map[Kind]int {
	m := map[Kind]int{}
	for _, r := range recs {
		m[r.Kind]++
	}
	return m
}

func TestReadFixture(t *testing.T) {
	recs, next, err := ReadFrom(fixture, 0, 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(fixture)
	if next != info.Size() {
		t.Fatalf("next offset %d, want file size %d", next, info.Size())
	}
	c := count(recs)
	// 6 prompts (5 typed, 1 harness task notification), 6 replies,
	// 7 main-thread tool calls with 7 results (the subagent's own tool
	// call lives in its own transcript).
	if c[Prompt] != 6 || c[Text] != 6 || c[ToolUse] != 7 || c[ToolResult] != 7 {
		t.Fatalf("kinds = %v", c)
	}
	var rejected *Record
	for i, r := range recs {
		if r.Kind == ToolResult && strings.HasSuffix(r.ToolID, "CXbaZd") {
			rejected = &recs[i]
		}
		if r.Kind == Text && r.PromptID != "" {
			t.Errorf("assistant record has a promptId: %+v", r)
		}
		if r.TS.IsZero() {
			t.Errorf("record without timestamp: %+v", r)
		}
	}
	if rejected == nil || !rejected.IsError || !strings.Contains(string(rejected.Output), "Blocked") {
		t.Fatalf("harness-rejected tool result not parsed: %+v", rejected)
	}
}

func TestReadResumesAndSkipsPartialLine(t *testing.T) {
	data, _ := os.ReadFile(fixture)
	lines := strings.SplitAfter(string(data), "\n")
	path := filepath.Join(t.TempDir(), "t.jsonl")
	// Two complete lines plus half of the third, as if mid-write.
	half := lines[2][:len(lines[2])/2]
	os.WriteFile(path, []byte(lines[0]+lines[1]+half), 0o600)

	recs1, off1, err := ReadFrom(path, 0, 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	if off1 != int64(len(lines[0])+len(lines[1])) {
		t.Fatalf("offset %d stops inside or past the partial line", off1)
	}
	os.WriteFile(path, []byte(strings.Join(lines, "")), 0o600)
	recs2, _, _ := ReadFrom(path, off1, 16<<20)
	all, _, _ := ReadFrom(path, 0, 16<<20)
	if len(recs1)+len(recs2) != len(all) {
		t.Fatalf("resumed reads gave %d records, one full read %d", len(recs1)+len(recs2), len(all))
	}
}

func TestReadRestartsWhenFileShrinks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	data, _ := os.ReadFile(fixture)
	first := strings.SplitAfter(string(data), "\n")[0]
	os.WriteFile(path, []byte(first), 0o600)
	recs, off, err := ReadFrom(path, 1<<20, 16<<20)
	if err != nil || off != int64(len(first)) || len(recs) == 0 {
		t.Fatalf("recs=%d off=%d err=%v", len(recs), off, err)
	}
}

func TestParseLineIgnoresNoise(t *testing.T) {
	for _, l := range []string{`not json`, `{"type":"attachment"}`, `{"type":"assistant","isSidechain":true,"message":{"content":[{"type":"text","text":"x"}]}}`} {
		if recs := parseLine([]byte(l)); len(recs) != 0 {
			t.Errorf("%s gave %v", l, recs)
		}
	}
}

func TestLineOverMaxLineSkipped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	f, _ := os.Create(path)
	f.WriteString(`{"type":"user","message":{"content":"`)
	f.Write(make([]byte, MaxLine)) // zero bytes; content does not matter
	f.WriteString("\"}}\n" + `{"type":"user","promptId":"p","message":{"content":"after"}}` + "\n")
	f.Close()
	recs, off, err := ReadFrom(path, 0, 1<<20)
	if err != nil || len(recs) != 1 || recs[0].Kind != Skipped || recs[0].Bytes <= MaxLine {
		t.Fatalf("recs=%+v err=%v; want one Skipped record", recs, err)
	}
	if recs, _, _ = ReadFrom(path, off, 1<<20); len(recs) != 1 || recs[0].Text != "after" {
		t.Fatalf("line after the skipped one not read: %+v", recs)
	}
}

func TestLongLineReadAlone(t *testing.T) {
	data, _ := os.ReadFile(fixture)
	normal := strings.SplitAfter(string(data), "\n")[0]
	huge := `{"type":"user","message":{"content":"` + strings.Repeat("A", 3000) + `"}}` + "\n"
	path := filepath.Join(t.TempDir(), "t.jsonl")
	os.WriteFile(path, []byte(huge+normal), 0o600)

	const max = 1024 // smaller than the long line, which is still under MaxLine
	recs, off, err := ReadFrom(path, 0, max)
	if err != nil || len(recs) != 1 || recs[0].Kind != Prompt || off != int64(len(huge)) {
		t.Fatalf("recs=%+v off=%d err=%v; want the long line read on its own", recs, off, err)
	}
	recs, _, _ = ReadFrom(path, off, max)
	if len(recs) == 0 || recs[0].Kind != Prompt {
		t.Fatalf("line after the huge one not read: %+v", recs)
	}
}

func TestHugeLineStillBeingWrittenWaits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	os.WriteFile(path, []byte(strings.Repeat("A", 3000)), 0o600) // no newline yet
	recs, off, err := ReadFrom(path, 0, 1024)
	if err != nil || len(recs) != 0 || off != 0 {
		t.Fatalf("recs=%v off=%d err=%v; want to wait at offset 0", recs, off, err)
	}
}

func TestAssistantModel(t *testing.T) {
	recs, _, _ := ReadFrom(fixture, 0, 16<<20)
	for _, r := range recs {
		switch r.Kind {
		case Text, ToolUse:
			if r.Model != "claude-haiku-5-5" {
				t.Fatalf("assistant record model = %q", r.Model)
			}
		case Prompt, ToolResult:
			if r.Model != "" {
				t.Fatalf("user record has a model: %+v", r)
			}
		}
	}
	syn := `{"type":"assistant","message":{"model":"<synthetic>","content":[{"type":"text","text":"x"}]}}`
	if recs := parseLine([]byte(syn)); len(recs) != 1 || recs[0].Model != "" {
		t.Fatalf("synthetic model kept: %+v", recs)
	}
}
