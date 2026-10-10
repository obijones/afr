package rules

import (
	"encoding/json"
	"path"
	"strings"
)

// input is a tool call reduced to what rules match on.
type input struct {
	tool    string
	command string    // shell command text, if the tool runs one
	segs    []segment // the command split into simple commands
	reads   []string  // paths read (normalized)
	writes  []string  // paths written: file tools, redirects, tee, cp, ...
	content string    // text written by a file tool
	byTool  bool      // writes came from a file-writing tool
}

// Tools that write a file named by file_path or notebook_path.
var writeTools = map[string]bool{"Write": true, "Edit": true, "MultiEdit": true, "NotebookEdit": true}

// Tools that read a path.
var readTools = map[string]bool{"Read": true, "Grep": true, "Glob": true}

func parseInput(tool string, raw json.RawMessage, cwd string, c *Context) input {
	in := input{tool: tool}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return in
	}
	str := func(k string) string { s, _ := m[k].(string); return s }

	// Any tool with a command string runs a shell command: Bash,
	// PowerShell, Monitor, and other harnesses' shell tools.
	if cmd := str("command"); cmd != "" {
		in.command = cmd
		in.segs = splitCommand(cmd, 0)
		for _, s := range in.segs {
			for _, t := range s.writeTargets() {
				in.writes = append(in.writes, normPath(t, cwd, c))
			}
		}
	}
	p := str("file_path")
	if p == "" {
		p = str("notebook_path")
	}
	switch {
	case writeTools[tool]:
		if p != "" {
			in.writes = append(in.writes, normPath(p, cwd, c))
			in.byTool = true
		}
		var b strings.Builder
		for _, k := range []string{"content", "new_string", "new_source"} {
			b.WriteString(str(k))
			b.WriteByte('\n')
		}
		if edits, ok := m["edits"].([]any); ok {
			for _, e := range edits {
				if em, ok := e.(map[string]any); ok {
					s, _ := em["new_string"].(string)
					b.WriteString(s)
					b.WriteByte('\n')
				}
			}
		}
		in.content = b.String()
	case readTools[tool]:
		for _, k := range []string{"file_path", "path", "pattern"} {
			if s := str(k); s != "" && (k != "pattern" || tool == "Glob") {
				in.reads = append(in.reads, normPath(s, cwd, c))
			}
		}
	}
	return in
}

// normPath puts a path in one comparable form whatever OS wrote it:
// forward slashes, ~ and home variables expanded, relative paths joined
// to cwd, cleaned, and lowercased when it is a Windows path.
func normPath(p, cwd string, c *Context) string {
	p = strings.Trim(strings.TrimSpace(p), `"'`)
	if p == "" {
		return ""
	}
	home := ""
	tmp := "/tmp"
	if c != nil {
		home = c.Home
		if len(c.TempDirs) > 0 {
			tmp = c.TempDirs[0]
		}
	}
	for _, v := range []string{"$HOME", "${HOME}", "%USERPROFILE%", "$env:USERPROFILE", "$env:HOME", "~"} {
		if home != "" && hasPrefixFold(p, v) && (len(p) == len(v) || p[len(v)] == '/' || p[len(v)] == '\\') {
			p = home + p[len(v):]
			break
		}
	}
	for _, v := range []string{"$TMPDIR", "${TMPDIR}", "%TEMP%", "%TMP%", "$env:TEMP", "$env:TMP"} {
		if hasPrefixFold(p, v) && (len(p) == len(v) || p[len(v)] == '/' || p[len(v)] == '\\') {
			p = tmp + p[len(v):]
			break
		}
	}
	p = strings.ReplaceAll(p, `\`, "/")
	if !isAbs(p) && cwd != "" {
		p = strings.ReplaceAll(cwd, `\`, "/") + "/" + p
	}
	if strings.HasPrefix(p, "//") {
		p = "//" + strings.TrimPrefix(path.Clean(p[1:]), "/") // keep a UNC prefix
	} else {
		p = path.Clean(p)
	}
	if isWindowsPath(p) {
		p = strings.ToLower(p)
	}
	return p
}

// hasDrive reports a Windows drive prefix such as C: or C:/.
func hasDrive(p string) bool {
	if len(p) < 2 || p[1] != ':' {
		return false
	}
	c := p[0] | 0x20
	return c >= 'a' && c <= 'z' && (len(p) == 2 || p[2] == '/')
}

func isAbs(p string) bool { return strings.HasPrefix(p, "/") || hasDrive(p) }

func isWindowsPath(p string) bool { return hasDrive(p) || strings.HasPrefix(p, "//") }

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// under reports whether p is dir or inside it. Both must be normalized.
func under(p, dir string) bool {
	if p == "" || dir == "" {
		return false
	}
	dir = strings.TrimSuffix(dir, "/")
	return p == dir || strings.HasPrefix(p, dir+"/")
}

// segment is one simple command: words with quotes removed, plus the
// redirect targets that belong to it.
type segment struct {
	raw     string
	words   []string
	outputs []string // > and >> targets
	inputs  []string // < sources
	piped   bool     // stdin comes from a pipe
}

// name returns the command word, after env assignments and wrappers such
// as sudo, as a lowercase base name without .exe, and its arguments.
func (s segment) name() (string, []string) {
	w := s.words
	for len(w) > 0 {
		cmd := baseName(w[0])
		switch {
		case strings.Contains(w[0], "=") && !strings.HasPrefix(w[0], "-") && !strings.HasPrefix(w[0], "="):
			w = w[1:] // VAR=value
		case cmd == "sudo" || cmd == "doas" || cmd == "nohup" || cmd == "time" || cmd == "exec" ||
			cmd == "command" || cmd == "nice" || cmd == "stdbuf" || cmd == "timeout":
			w = w[1:]
			for len(w) > 0 && strings.HasPrefix(w[0], "-") {
				takesArg := w[0] == "-u" || w[0] == "-g" || w[0] == "-C" || w[0] == "-n" || w[0] == "-p"
				w = w[1:]
				if takesArg && len(w) > 0 {
					w = w[1:]
				}
			}
			if cmd == "timeout" && len(w) > 0 {
				w = w[1:] // the duration
			}
		default:
			return cmd, w[1:]
		}
	}
	return "", nil
}

// first returns the first word as written, lowercased base name: the
// wrapper itself when there is one (sudo, su, doas).
func (s segment) first() string {
	for _, w := range s.words {
		if strings.Contains(w, "=") && !strings.HasPrefix(w, "-") {
			continue
		}
		return baseName(w)
	}
	return ""
}

func baseName(w string) string {
	w = strings.ReplaceAll(w, `\`, "/")
	if i := strings.LastIndex(w, "/"); i >= 0 {
		w = w[i+1:]
	}
	return strings.TrimSuffix(strings.ToLower(w), ".exe")
}

// positional returns the arguments that are not flags.
func positional(args []string) []string {
	var out []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") || a == "-" {
			out = append(out, a)
		}
	}
	return out
}

// flagValue returns the value after a PowerShell or long-form flag such
// as -Path X or --output=X, matching names case-insensitively.
func flagValue(args []string, names ...string) string {
	for i, a := range args {
		for _, n := range names {
			if strings.EqualFold(a, n) && i+1 < len(args) {
				return args[i+1]
			}
			if hasPrefixFold(a, n+"=") || hasPrefixFold(a, n+":") {
				return a[len(n)+1:]
			}
		}
	}
	return ""
}

// writeTargets lists the files a segment writes: redirects, tee, cp, mv,
// ln, install, sed -i, dd of=, and the PowerShell content cmdlets.
func (s segment) writeTargets() []string {
	out := append([]string(nil), s.outputs...)
	cmd, args := s.name()
	pos := positional(args)
	switch cmd {
	case "tee":
		out = append(out, pos...)
	case "cp", "mv", "ln", "install", "rsync":
		if len(pos) >= 2 {
			out = append(out, pos[len(pos)-1])
		}
	case "sed":
		if (hasFlag(args, "-i") || hasLong(args, "--in-place")) && len(pos) >= 2 {
			out = append(out, pos[1:]...)
		}
	case "dd":
		for _, a := range args {
			if strings.HasPrefix(a, "of=") {
				out = append(out, a[3:])
			}
		}
	case "out-file", "set-content", "add-content", "new-item", "ni", "sc", "ac":
		if v := flagValue(args, "-FilePath", "-Path", "-LiteralPath"); v != "" {
			out = append(out, v)
		} else if len(pos) > 0 {
			out = append(out, pos[0])
		}
	case "copy-item", "move-item", "copy", "move", "cpi", "mi", "xcopy", "robocopy":
		if v := flagValue(args, "-Destination"); v != "" {
			out = append(out, v)
		} else if len(pos) >= 2 {
			out = append(out, pos[len(pos)-1])
		}
	}
	var keep []string
	for _, t := range out {
		l := strings.ToLower(t)
		if t == "" || strings.HasPrefix(t, "&") || strings.HasPrefix(l, "/dev/null") || strings.HasPrefix(l, "/dev/std") ||
			l == "/dev/tty" || l == "nul" || l == "$null" || l == "-" {
			continue
		}
		keep = append(keep, t)
	}
	return keep
}

// hasFlag reports a short flag, alone or combined (-i, -ri, -i.bak).
func hasFlag(args []string, flag string) bool {
	letter := strings.TrimPrefix(flag, "-")
	for _, a := range args {
		if a == flag || (strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a[1:], letter)) {
			return true
		}
	}
	return false
}

// hasLong reports a long flag, alone or with a value (--force, --force=x).
func hasLong(args []string, flag string) bool {
	for _, a := range args {
		if a == flag || strings.HasPrefix(a, flag+"=") {
			return true
		}
	}
	return false
}

// splitCommand splits shell text into simple commands on unquoted |, ||,
// &&, ;, &, and newlines, removing quotes and collecting redirects. The
// bodies of sh -c, bash -c, powershell -Command, and cmd /c are split
// too, so commands wrapped in another shell are still seen.
func splitCommand(text string, depth int) []segment {
	var segs []segment
	cur := segment{}
	var word strings.Builder
	inWord := false
	redirect := byte(0) // '>' or '<' when the next word is a redirect target
	var quote rune
	start := 0

	endWord := func() {
		if !inWord {
			return
		}
		w := word.String()
		switch redirect {
		case '>':
			cur.outputs = append(cur.outputs, w)
		case '<':
			cur.inputs = append(cur.inputs, w)
		default:
			cur.words = append(cur.words, w)
		}
		redirect = 0
		word.Reset()
		inWord = false
	}
	endSeg := func(end int, piped bool) {
		endWord()
		cur.raw = strings.TrimSpace(text[start:end])
		if len(cur.words) > 0 || len(cur.outputs) > 0 {
			segs = append(segs, cur)
		}
		cur = segment{piped: piped}
	}

	rs := []rune(text)
	byteAt := func(i int) int { return len(string(rs[:i])) }
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if quote != 0 {
			if r == quote {
				quote = 0
			} else if r == '\\' && quote == '"' && i+1 < len(rs) && (rs[i+1] == '"' || rs[i+1] == '\\') {
				i++
				word.WriteRune(rs[i])
			} else {
				word.WriteRune(r)
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
			inWord = true
		case ' ', '\t', '\r':
			endWord()
		case '\n', ';':
			endSeg(byteAt(i), false)
			start = byteAt(i + 1)
		case '|':
			piped := true
			if i+1 < len(rs) && rs[i+1] == '|' {
				i++
				piped = false
			}
			endSeg(byteAt(i), piped)
			start = byteAt(i + 1)
		case '&':
			switch {
			case i+1 < len(rs) && rs[i+1] == '&':
				endSeg(byteAt(i), false)
				i++
				start = byteAt(i + 1)
			case i+1 < len(rs) && rs[i+1] == '>':
				endWord() // &> file
			case inWord || (i > 0 && rs[i-1] == '>'):
				word.WriteRune(r) // 2>&1 target, or a & inside a word
				inWord = true
			default:
				endSeg(byteAt(i), false)
				start = byteAt(i + 1)
			}
		case '>', '<':
			// A word made only of digits before > is a file descriptor.
			if inWord {
				w := word.String()
				if strings.Trim(w, "0123456789") == "" {
					word.Reset()
					inWord = false
				} else {
					endWord()
				}
			}
			// <( and >( are process substitution: keep as text.
			if i+1 < len(rs) && rs[i+1] == '(' {
				word.WriteRune(r)
				inWord = true
				continue
			}
			redirect = byte(r)
			for i+1 < len(rs) && (rs[i+1] == '>' || rs[i+1] == '|') {
				i++
			}
		case '\\':
			if i+1 < len(rs) && rs[i+1] == '\n' {
				i++ // line continuation
				continue
			}
			word.WriteRune(r)
			inWord = true
		default:
			word.WriteRune(r)
			inWord = true
		}
	}
	endSeg(len(text), false)

	if depth < 3 {
		var nested []segment
		for _, s := range segs {
			if body := shellBody(s); body != "" {
				nested = append(nested, splitCommand(body, depth+1)...)
			}
		}
		segs = append(segs, nested...)
	}
	return segs
}

// shellBody returns the command text a shell wrapper runs, if any.
func shellBody(s segment) string {
	cmd, args := s.name()
	switch cmd {
	case "sh", "bash", "zsh", "dash", "ksh":
		for i, a := range args {
			if strings.HasPrefix(a, "-") && strings.Contains(a, "c") && !strings.HasPrefix(a, "--") && i+1 < len(args) {
				return args[i+1]
			}
		}
	case "powershell", "pwsh":
		if v := flagValue(args, "-Command", "-c"); v != "" {
			return v
		}
	case "cmd":
		for i, a := range args {
			if strings.EqualFold(a, "/c") || strings.EqualFold(a, "/k") {
				return strings.Join(args[i+1:], " ")
			}
		}
	}
	return ""
}
