package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"testing"
	"time"
)

// TestHookLatency is the latency gate from the build plan: the agent waits
// for every hook call, so p95 of one `afr hook` process must stay within
// budget. It builds the real binary and runs it on the captured Claude
// Code payloads, with transcript catch-up reading a real transcript.
//
// Opt-in, because timing depends on the machine: AFR_LATENCY=1. The budget
// is AFR_LATENCY_BUDGET_MS (default 50). It times afr itself, exec'd
// directly; Claude Code adds its own shell start-up on top.
func TestHookLatency(t *testing.T) {
	if os.Getenv("AFR_LATENCY") != "1" {
		t.Skip("set AFR_LATENCY=1 to run the latency gate")
	}
	budget := 50 * time.Millisecond
	if v := os.Getenv("AFR_LATENCY_BUDGET_MS"); v != "" {
		ms, err := strconv.Atoi(v)
		if err != nil {
			t.Fatalf("AFR_LATENCY_BUDGET_MS: %v", err)
		}
		budget = time.Duration(ms) * time.Millisecond
	}

	work := t.TempDir()
	bin := filepath.Join(work, "afr")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	const fixtures = "../../testdata/claude-code/2.1.295"
	transcript := filepath.Join(work, "transcript.jsonl")
	data, err := os.ReadFile(filepath.Join(fixtures, "transcript.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, data, 0o600); err != nil {
		t.Fatal(err)
	}
	names, _ := filepath.Glob(filepath.Join(fixtures, "*.json"))
	var payloads [][]byte
	for _, n := range names {
		b, err := os.ReadFile(n)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		m["transcript_path"] = transcript // catch-up hooks read a real file
		b, _ = json.Marshal(m)
		payloads = append(payloads, b)
	}
	if len(payloads) == 0 {
		t.Fatal("no fixtures found")
	}

	run := func(p []byte, home string) time.Duration {
		cmd := exec.Command(bin, "hook")
		cmd.Env = append(os.Environ(), "AFR_HOME="+home)
		cmd.Stdin = bytes.NewReader(p)
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		start := time.Now()
		if err := cmd.Run(); err != nil {
			t.Fatalf("afr hook: %v", err)
		}
		d := time.Since(start)
		if stdout.Len() != 0 {
			t.Fatalf("afr hook wrote to stdout: %q", stdout.String())
		}
		return d
	}

	home := filepath.Join(work, "data")
	for i := 0; i < 10; i++ { // warm the binary and the file cache
		run(payloads[i%len(payloads)], home)
	}
	const n = 300
	times := make([]time.Duration, n)
	for i := range times {
		times[i] = run(payloads[i%len(payloads)], home)
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	pct := func(q float64) time.Duration { return times[min(len(times)-1, int(float64(len(times))*q))] }
	p50, p95, p99 := pct(0.50), pct(0.95), pct(0.99)
	t.Logf("afr hook latency on %s/%s over %d calls: p50 %v  p95 %v  p99 %v  max %v (budget p95 %v)",
		runtime.GOOS, runtime.GOARCH, n, p50.Round(100*time.Microsecond), p95.Round(100*time.Microsecond),
		p99.Round(100*time.Microsecond), times[n-1].Round(100*time.Microsecond), budget)
	if p95 > budget {
		t.Fatalf("p95 %v exceeds the %v budget", p95, budget)
	}
	if log, err := os.ReadFile(filepath.Join(home, "afr-errors.log")); err == nil && len(log) > 0 {
		t.Fatalf("afr logged errors during the run:\n%s", log)
	}
}
