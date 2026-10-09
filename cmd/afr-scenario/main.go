// Command afr-scenario checks a recorded session against the seeded
// scenario's expected results. It is a development tool, run by
// scripts/seeded-scenario.sh.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/obijones/afr/internal/scenario"
	"github.com/obijones/afr/internal/store"
	"github.com/obijones/afr/internal/view"
)

func main() {
	data := flag.String("data", "", "afr data directory holding the session")
	session := flag.String("session", "", "session id (or unique prefix)")
	expect := flag.String("expect", "scripts/seeded-scenario.expected.json", "expected-results file")
	flag.Parse()
	if *data == "" || *session == "" {
		fmt.Fprintln(os.Stderr, "usage: afr-scenario -data <dir> -session <id> [-expect <file>]")
		os.Exit(2)
	}

	x, err := scenario.Load(*expect)
	if err != nil {
		fail(err)
	}
	path, err := view.Resolve(*data, *session)
	if err != nil {
		fail(err)
	}
	events, err := view.Load(path)
	if err != nil {
		fail(err)
	}
	probs, err := store.Verify(path)
	if err != nil {
		fail(err)
	}

	results := scenario.Check(x, events, len(probs) == 0)
	counts := map[string]int{}
	for _, r := range results {
		counts[r.Status]++
		fmt.Printf("%-7s  %s\n", r.Status, r.Check)
	}
	fmt.Printf("\n%d passed, %d failed, %d pending\n", counts["PASS"], counts["FAIL"], counts["PENDING"])
	if scenario.Failed(results) {
		os.Exit(1)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "afr-scenario:", err)
	os.Exit(1)
}
