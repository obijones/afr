// Command afr records AI coding agent sessions for later review.
package main

import (
	"fmt"
	"io"
	"os"
)

var version = "0.0.0-dev"

const usage = `usage: afr <command>

commands:
  hook      read one agent hook payload on stdin and record it (always exits 0)
  version   print the afr version
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run never returns 2, even for usage errors: afr runs as an agent hook,
// and Claude Code treats exit 2 from PreToolUse as a block, so a mistyped
// subcommand in a hook config must not stop the agent.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 1
	}
	switch args[0] {
	case "hook":
		return runHook(stdin)
	case "version":
		fmt.Fprintln(stdout, version)
		return 0
	default:
		fmt.Fprintf(stderr, "afr: unknown command %q\n\n%s", args[0], usage)
		return 1
	}
}
