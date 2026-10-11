package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/obijones/afr/internal/platform"
	"github.com/obijones/afr/internal/rules"
	"github.com/obijones/afr/internal/schema"
	"github.com/obijones/afr/internal/session"
)

const rulesUsage = `usage: afr rules <list | check | test>

  afr rules list                 every active rule, its severity, and where it comes from
  afr rules check [file ...]     validate rule files (default: the active rules folder)
  afr rules test [flags] [cmd]   show what the rules flag for a command or file access
      --file F     use this rule file instead of the active folder (repeatable)
      --tool T     tool name (default Bash for a command, Read or Write for paths)
      --read P     a file read
      --write P    a file written
      --content T  text written or shown
`

func runRules(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, rulesUsage)
		return 1
	}
	dataDir, err := platform.DataDir()
	if err != nil {
		fmt.Fprintln(stderr, "afr:", err)
		return 1
	}
	switch args[0] {
	case "list":
		return rulesList(dataDir, stdout, stderr)
	case "check":
		return rulesCheck(dataDir, args[1:], stdout, stderr)
	case "test":
		return rulesTest(dataDir, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "afr: unknown rules command %q\n\n%s", args[0], rulesUsage)
		return 1
	}
}

func printProblems(w io.Writer, problems []error) {
	for _, p := range problems {
		fmt.Fprintln(w, "problem:", p)
	}
}

func rulesList(dataDir string, stdout, stderr io.Writer) int {
	set, problems := rules.LoadDirs(rules.Dirs(dataDir)...)
	printProblems(stderr, problems)
	if set != nil {
		fmt.Fprintf(stdout, "customer rules from %s (rules version %s)\n\n", set.Dir, set.Version())
	} else {
		fmt.Fprintf(stdout, "built-in rules only (rules version %s); customer rules go in %s or %s\n\n",
			rules.Version, rules.Dirs(dataDir)[0], rules.Dirs(dataDir)[1])
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RULE\tSEVERITY\tSOURCE")
	for _, r := range rules.Catalog {
		sev, src := r.Tiers, "built-in"
		if set != nil {
			if o, ok := set.Overrides[r.ID]; ok {
				src = "built-in, overridden in " + o.Source
				sev = o.Severity
				if o.Disabled {
					sev = "disabled"
				}
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", r.ID, tierWords(sev), src)
	}
	if set != nil {
		for _, r := range set.Rules {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", r.ID, r.Severity, r.Source)
		}
	}
	tw.Flush()
	if len(problems) > 0 {
		return 1
	}
	return 0
}

// tierWords shortens a built-in tier description to its severities.
func tierWords(tiers string) string {
	var out []string
	for _, s := range []string{rules.High, rules.Medium, rules.Low, "disabled"} {
		if strings.Contains(tiers, s) {
			out = append(out, s)
		}
	}
	return strings.Join(out, "/")
}

func rulesCheck(dataDir string, files []string, stdout, stderr io.Writer) int {
	var set *rules.Set
	var problems []error
	if len(files) > 0 {
		set, problems = rules.LoadFiles("", files)
	} else {
		set, problems = rules.LoadDirs(rules.Dirs(dataDir)...)
	}
	printProblems(stdout, problems)
	switch {
	case set == nil && len(problems) == 0:
		fmt.Fprintln(stdout, "no customer rule files found in", strings.Join(rules.Dirs(dataDir), " or "))
	case set != nil:
		fmt.Fprintf(stdout, "%d files, %d custom rules, %d overrides loaded (rules version %s)\n",
			len(set.Files), len(set.Rules), len(set.Overrides), set.Version())
	}
	if len(problems) > 0 {
		return 1
	}
	return 0
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func rulesTest(dataDir string, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("rules test", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var files multiFlag
	fs.Var(&files, "file", "rule file to use instead of the active folder (repeatable)")
	tool := fs.String("tool", "", "tool name")
	read := fs.String("read", "", "a file read")
	write := fs.String("write", "", "a file written")
	content := fs.String("content", "", "text written or shown")
	if fs.Parse(args) != nil {
		return 1
	}
	command := strings.Join(fs.Args(), " ")
	if command == "" && *read == "" && *write == "" && *content == "" {
		fmt.Fprint(stderr, rulesUsage)
		return 1
	}

	cwd, _ := os.Getwd()
	ctx, problems := session.Context(cwd, dataDir)
	if len(files) > 0 {
		ctx.Custom, problems = rules.LoadFiles("", files)
	}
	printProblems(stderr, problems)

	input := map[string]any{}
	name := *tool
	switch {
	case command != "":
		input["command"] = command
		if name == "" {
			name = "Bash"
		}
	case *write != "":
		if name == "" {
			name = "Write"
		}
	case *read != "":
		if name == "" {
			name = "Read"
		}
	}
	if *write != "" {
		input["file_path"] = *write
		input["content"] = *content
	} else if *read != "" {
		input["file_path"] = *read
	}
	var e schema.Event
	if name != "" {
		b, _ := json.Marshal(input)
		e = schema.Event{EventType: schema.ToolCall, Tool: name, Cwd: cwd, Content: &schema.Content{Input: b}}
	} else {
		e = schema.Event{EventType: schema.AssistantMessage, Content: &schema.Content{Text: *content}}
	}
	hs, err := rules.Evaluate(e, &ctx)
	if err != nil {
		fmt.Fprintln(stderr, "problem:", err)
	}
	if len(hs) == 0 {
		fmt.Fprintln(stdout, "no highlights")
	}
	for _, h := range hs {
		fmt.Fprintf(stdout, "%-6s  %s  %s\n", strings.ToUpper(h.Severity), h.RuleID, h.Detail)
	}
	if len(problems) > 0 || err != nil {
		return 1
	}
	return 0
}
