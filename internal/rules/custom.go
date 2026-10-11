package rules

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/obijones/afr/internal/platform"
	"github.com/obijones/afr/internal/schema"
	"go.yaml.in/yaml/v3"
)

// Limits on customer rule files, so a mistake cannot slow every hook call.
const (
	maxRuleFiles    = 32
	maxRuleFileSize = 256 << 10
	maxCustomRules  = 500
)

// File is one customer rule file (YAML).
//
//	rules:
//	  - id: acme.prod_db_access
//	    title: Command touches the production database
//	    severity: high
//	    match:
//	      command: '(?i)\bpsql\b.*prod-db'
//	overrides:
//	  secrets.env_dump: {disabled: true}
type File struct {
	Rules     []CustomRule        `yaml:"rules"`
	Overrides map[string]Override `yaml:"overrides"`
}

// CustomRule tags a tool call when every match field given matches.
type CustomRule struct {
	ID          string   `yaml:"id"`
	Title       string   `yaml:"title"`
	Severity    string   `yaml:"severity"`
	Description string   `yaml:"description"`
	References  []string `yaml:"references"`
	Match       Match    `yaml:"match"`

	Source string `yaml:"-"` // file it came from
	m      compiledMatch
}

// Match fields. Each takes one pattern or a list (any one may match);
// all fields given must match. command, tool, and content are regular
// expressions (Go RE2 syntax); read_path and write_path are globs where
// * stays within a folder, ** crosses folders, and a glob without a
// leading / or drive matches at any depth. Paths compare with forward
// slashes and ignore case.
type Match struct {
	Tool      patternList `yaml:"tool"`       // tool name, such as Bash or Write
	Command   patternList `yaml:"command"`    // shell command text
	ReadPath  patternList `yaml:"read_path"`  // files read by Read, Grep, Glob
	WritePath patternList `yaml:"write_path"` // files written by file tools or shell commands
	Content   patternList `yaml:"content"`    // tool input, tool output, prompts, and replies
}

// patternList accepts a single string or a list of strings.
type patternList []string

func (p *patternList) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		*p = patternList{n.Value}
		return nil
	case yaml.SequenceNode:
		var l []string
		if err := n.Decode(&l); err != nil {
			return err
		}
		*p = l
		return nil
	}
	return fmt.Errorf("line %d: want a pattern or a list of patterns", n.Line)
}

// Override changes a built-in rule.
type Override struct {
	Severity string `yaml:"severity"`
	Disabled bool   `yaml:"disabled"`

	Source string `yaml:"-"`
}

type compiledMatch struct {
	tool, command, read, write, content []*regexp.Regexp
}

// Set is the customer rules in force: custom rules and overrides from
// one rules folder, with a hash that identifies them.
type Set struct {
	Rules     []CustomRule
	Overrides map[string]Override
	Dir       string   // folder the rules came from
	Files     []string // files loaded
	hash      string
}

// Version is the rules version stamped on events: the built-in version,
// plus a short hash of the customer rule files when there are any.
func (s *Set) Version() string {
	if s == nil || s.hash == "" {
		return Version
	}
	return Version + "+" + s.hash
}

// validID reports an id like org.rule_name: lowercase letters, digits,
// _ and -, with at least one dot that is not first.
func validID(id string) bool {
	dot := strings.IndexByte(id, '.')
	if dot <= 0 || dot == len(id)-1 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

// ManagedDir is the administrator's rules folder; a variable for tests.
var ManagedDir = platform.ManagedRulesDir()

// Dirs lists where customer rules are looked for, in priority order: the
// managed folder, then <data dir>/rules.
func Dirs(dataDir string) []string {
	return []string{ManagedDir, filepath.Join(dataDir, "rules")}
}

// LoadDirs loads customer rules from the first folder in dirs that holds
// any rule file, so a managed folder, listed first, replaces personal
// rules entirely and users cannot loosen it. Problems are returned with
// the set: a bad rule or file is skipped, the rest still load.
func LoadDirs(dirs ...string) (*Set, []error) {
	for _, d := range dirs {
		files := ruleFiles(d)
		if len(files) > 0 {
			return LoadFiles(d, files)
		}
	}
	return nil, nil
}

// ruleFiles lists *.yaml and *.yml files in dir, sorted.
func ruleFiles(dir string) []string {
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		if e.Type().IsRegular() && (strings.HasSuffix(n, ".yaml") || strings.HasSuffix(n, ".yml")) && !strings.HasPrefix(n, ".") {
			out = append(out, filepath.Join(dir, n))
		}
	}
	sort.Strings(out)
	return out
}

// LoadFiles loads and validates the given rule files.
func LoadFiles(dir string, files []string) (*Set, []error) {
	var errs []error
	s := &Set{Dir: dir, Overrides: map[string]Override{}}
	h := sha256.New()
	if len(files) > maxRuleFiles {
		errs = append(errs, fmt.Errorf("%s: %d rule files, only the first %d are loaded", dir, len(files), maxRuleFiles))
		files = files[:maxRuleFiles]
	}
	builtin := map[string]bool{}
	for _, r := range Catalog {
		builtin[r.ID] = true
	}
	for _, path := range files {
		b, err := readLimited(path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		fmt.Fprintf(h, "%s\x00%d\x00", filepath.Base(path), len(b))
		h.Write(b)
		var f File
		dec := yaml.NewDecoder(bytes.NewReader(b))
		dec.KnownFields(true) // a misspelled field is an error, not a silently ignored rule
		if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
			errs = append(errs, fmt.Errorf("%s: %v", path, err))
			continue
		}
		s.Files = append(s.Files, path)
		for _, r := range f.Rules {
			r.Source = path
			if err := r.compile(builtin); err != nil {
				errs = append(errs, fmt.Errorf("%s: rule %q: %v", path, r.ID, err))
				continue
			}
			if slices.ContainsFunc(s.Rules, func(o CustomRule) bool { return o.ID == r.ID }) {
				errs = append(errs, fmt.Errorf("%s: rule %q is defined twice", path, r.ID))
				continue
			}
			if len(s.Rules) >= maxCustomRules {
				errs = append(errs, fmt.Errorf("%s: more than %d custom rules; the rest are skipped", path, maxCustomRules))
				break
			}
			s.Rules = append(s.Rules, r)
		}
		for id, o := range f.Overrides {
			o.Source = path
			switch {
			case !builtin[id]:
				errs = append(errs, fmt.Errorf("%s: override %q: no built-in rule with that id", path, id))
			case !o.Disabled && Rank(o.Severity) == 0:
				errs = append(errs, fmt.Errorf("%s: override %q: severity must be high, medium, or low (or set disabled: true)", path, id))
			default:
				s.Overrides[id] = o
			}
		}
	}
	if len(s.Files) == 0 {
		return nil, errs
	}
	s.hash = hex.EncodeToString(h.Sum(nil))[:8]
	return s, errs
}

func readLimited(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b := make([]byte, maxRuleFileSize+1)
	n, _ := f.Read(b)
	for n < len(b) {
		m, err := f.Read(b[n:])
		n += m
		if err != nil || m == 0 {
			break
		}
	}
	if n > maxRuleFileSize {
		return nil, fmt.Errorf("%s: larger than %d KB; not loaded", path, maxRuleFileSize>>10)
	}
	return b[:n], nil
}

func (r *CustomRule) compile(builtin map[string]bool) error {
	switch {
	case !validID(r.ID):
		return fmt.Errorf("id must look like org.rule_name (lowercase, with a dot)")
	case builtin[r.ID]:
		return fmt.Errorf("id is a built-in rule; use overrides to change it")
	case Rank(r.Severity) == 0:
		return fmt.Errorf("severity must be high, medium, or low")
	}
	m := r.Match
	if len(m.Tool)+len(m.Command)+len(m.ReadPath)+len(m.WritePath)+len(m.Content) == 0 {
		return fmt.Errorf("match needs at least one of tool, command, read_path, write_path, content")
	}
	var err error
	re := func(ps patternList) []*regexp.Regexp {
		var out []*regexp.Regexp
		for _, p := range ps {
			x, e := regexp.Compile(p)
			if e != nil && err == nil {
				err = fmt.Errorf("pattern %q: %v", p, e)
			}
			out = append(out, x)
		}
		return out
	}
	glob := func(ps patternList) []*regexp.Regexp {
		var out []*regexp.Regexp
		for _, p := range ps {
			x, e := regexp.Compile(globRegexp(p))
			if e != nil && err == nil {
				err = fmt.Errorf("glob %q: %v", p, e)
			}
			out = append(out, x)
		}
		return out
	}
	r.m = compiledMatch{tool: re(m.Tool), command: re(m.Command), read: glob(m.ReadPath), write: glob(m.WritePath), content: re(m.Content)}
	return err
}

// globRegexp turns a path glob into an anchored, case-insensitive regexp.
// ** crosses folders, * and ? stay within one. A glob that does not start
// at a root (/ or a drive) matches at any depth; ~/ matches any home.
func globRegexp(g string) string {
	g = strings.ReplaceAll(g, `\`, "/")
	var b strings.Builder
	b.WriteString("(?i)")
	switch {
	case strings.HasPrefix(g, "~/"):
		b.WriteString("^(.*/)?")
		g = g[2:]
	case strings.HasPrefix(g, "/") || hasDrive(g):
		b.WriteString("^")
	case strings.HasPrefix(g, "**/"):
		b.WriteString("^(.*/)?")
		g = g[3:]
	default:
		b.WriteString("^(.*/)?")
	}
	for i := 0; i < len(g); i++ {
		switch c := g[i]; {
		case c == '*' && i+1 < len(g) && g[i+1] == '*':
			i++
			if i+1 < len(g) && g[i+1] == '/' {
				i++
				b.WriteString("(.*/)?")
			} else {
				b.WriteString(".*")
			}
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return b.String()
}

// evaluate applies one custom rule to an event.
func (r *CustomRule) evaluate(e schema.Event, in *input, set highlightSet) {
	m := r.m
	anyMatch := func(res []*regexp.Regexp, values ...string) (string, bool) {
		if len(res) == 0 {
			return "", true
		}
		for _, x := range res {
			for _, v := range values {
				if v != "" {
					if s := x.FindString(v); s != "" || x.MatchString(v) {
						return firstNonEmpty(s, v), true
					}
				}
			}
		}
		return "", false
	}
	var detail []string
	note := func(s string) {
		if s != "" && !slices.Contains(detail, s) {
			detail = append(detail, s)
		}
	}
	// Tool, command, and path fields only apply to tool calls.
	if len(m.tool)+len(m.command)+len(m.read)+len(m.write) > 0 {
		if e.EventType != schema.ToolCall || in == nil {
			return
		}
		if s, ok := anyMatch(m.tool, e.Tool); !ok {
			return
		} else if len(m.tool) > 0 {
			note(s)
		}
		if len(m.command) > 0 {
			s, ok := anyMatch(m.command, in.command)
			if !ok {
				return
			}
			note(s)
		}
		if len(m.read) > 0 {
			s, ok := anyMatch(m.read, in.reads...)
			if !ok {
				return
			}
			note(s)
		}
		if len(m.write) > 0 {
			s, ok := anyMatch(m.write, in.writes...)
			if !ok {
				return
			}
			note(s)
		}
	}
	if len(m.content) > 0 {
		var text string
		if c := e.Content; c != nil {
			text = c.Text + "\n" + string(c.Input) + "\n" + string(c.Output)
		}
		s, ok := anyMatch(m.content, text)
		if !ok {
			return
		}
		note(s)
	}
	d := strings.Join(detail, "; ")
	if r.Title != "" {
		d = r.Title + ": " + d
	}
	set.add(r.ID, r.Severity, d)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
