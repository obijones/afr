package rules

import (
	"regexp"
	"slices"
	"strings"
	"sync"
)

// Patterns are compiled on first use (see get), inside the hook's recovered path,
// never at package init: a bad pattern must cost highlights, not exit the
// hook with a panic (which a PreToolUse caller reads as a block).
var patterns = map[string]string{
	// 1: credentials. Matched on normalized paths and on command text.
	"cred.host":   `(?i)(^|[/\s"'=:])(\.ssh/[^\s"'|;&]*|\.aws/(credentials|sso/cache)|\.kube/config|\.config/gcloud/|\.azure/(accesstokens|msal_token_cache|azureprofile)|\.docker/config\.json|(\.|_)netrc\b|\.git-credentials|\.npmrc|\.pypirc|\.gnupg/|\.password-store/|\.config/gh/hosts\.yml|\.terraform\.d/credentials|\.vault-token)|/etc/g?shadow\b|keychains/|/microsoft/(credentials|protect)/|logins\.json|key4\.db|login data`,
	"cred.ssh.ok": `(?i)\.ssh/?([^\s"'|;&/]*/)*(known_hosts[^\s"'|;&]*|config|authorized_keys|[^\s"'|;&/]*\.pub|environment)?$`,
	"cred.cmd":    `(?i)\bsecurity\s+(find-(generic|internet)-password|dump-keychain|export)\b|\bsecret-tool\s+lookup\b|\bpass\s+show\b|\bop\s+(read|item\s+get)\b|\bcmdkey\s+/list\b|\bvaultcmd\b|\bGet-StoredCredential\b|\bmimikatz\b|\blsass\b`,
	"cred.env":    `(?i)(^|[/\s"'=:])\.env(rc|\.[a-z0-9_-]+)?($|[\s"';|&)])`,
	"cred.env.ok": `(?i)\.env\.(example|sample|template|dist|defaults)\b`,

	// 2: secret-shaped strings.
	"secret": `\b(AKIA|ASIA)[0-9A-Z]{16}\b|\b(ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{36}\b|\bgithub_pat_[A-Za-z0-9_]{22,}\b|\bglpat-[A-Za-z0-9_-]{20}\b|\bxox[abprs]-[A-Za-z0-9-]{10,}|hooks\.slack\.com/services/T[A-Z0-9]+/B[A-Z0-9]+/[A-Za-z0-9]+|-----BEGIN ((RSA|DSA|EC|OPENSSH|PGP|ENCRYPTED) )?PRIVATE KEY( BLOCK)?-----|\bAIza[0-9A-Za-z_-]{35}\b|\b[sr]k_live_[0-9A-Za-z]{20,}|\bsk-ant-[A-Za-z0-9_-]{20,}|\bsk-(proj-)?[A-Za-z0-9_-]{32,}|\bnpm_[A-Za-z0-9]{36}\b`,

	// 3: download piped to a shell.
	"dl.pipe":  `(?i)\b(curl|wget|fetch|iwr|irm|Invoke-WebRequest|Invoke-RestMethod)\b[^|;&]*\|\s*(sudo\s+(-\S+\s+)*)?((ba|z|da|k|fi)?sh|python[0-9.]*|perl|ruby|node|php|iex|Invoke-Expression|pwsh|powershell)\b`,
	"dl.subst": `(?i)\b((ba|z|da|k)?sh|source|\.)\s+(-c\s+)?["']?(\$\(|<\(|` + "`" + `)\s*(curl|wget)\b`,
	"dl.iex":   `(?i)\b(iex|Invoke-Expression)\b.*\b(iwr|irm|Invoke-WebRequest|Invoke-RestMethod|DownloadString|DownloadData)\b|\b(DownloadString|DownloadData)\b.*\b(iex|Invoke-Expression)\b`,

	// 4: decode and run.
	"dec.pipe":  `(?i)(\bbase64\s+(-\S*\s+)*(-d|--decode|-D)\b|\bxxd\s+(-\S+\s+)*-r\b|\bopenssl\s+[^|]*\s-d\b|\bcertutil\s+-decode\b)[^|;&]*\|\s*(sudo\s+)?((ba|z|da|k)?sh|python[0-9.]*|perl|ruby|node|php|iex|pwsh|powershell)\b`,
	"dec.eval":  `(?i)\beval\s+["']?(\$\(|` + "`" + `)[^)]*\bbase64\s+(-\S*\s+)*(-d|--decode|-D)\b|\b(exec|eval)\s*\(.*\bb(64|ase64)decode\b|\b(iex|Invoke-Expression|ScriptBlock\]::Create)\b.*\bFromBase64String\b|\bFromBase64String\b.*\b(iex|Invoke-Expression|ScriptBlock\]::Create)\b`,
	"dec.bare":  `(?i)\b(powershell|pwsh)(\.exe)?\b.*\s[-/](e|ec|en|enc|enco|encod|encode|encoded|encodedc|encodedco|encodedcom|encodedcomm|encodedcomma|encodedcomman|encodedcommand)\s+[A-Za-z0-9+/=]{16,}|\bFromBase64String\b`,
	"net.share": `(?i)\b(transfer\.sh|file\.io|0x0\.st|tmpfiles\.org|catbox\.moe|gofile\.io|pixeldrain\.com|bashupload\.com|oshi\.at|temp\.sh|pastebin\.com|paste\.ee|hastebin|dpaste|ghostbin|termbin\.com|ix\.io|sprunge\.us|rentry\.co|webhook\.site|requestbin|pipedream\.net|hookbin|beeceptor|discord(app)?\.com/api/webhooks|hooks\.slack\.com|api\.telegram\.org|ngrok\.io|ngrok-free\.app|dropbox(usercontent)?\.com|dropboxapi\.com|mega\.nz|wetransfer\.com|anonfiles|api\.github\.com/gists|gist\.github\.com|interactsh|oast\.(fun|pro|live|site|online|me)|burpcollaborator\.net)`,

	// 5: outbound transfer, matched per simple command.
	"up.curl":  `(?i)^(curl|curl\.exe)\b.*(\s(-d|--data(-binary|-raw|-urlencode|-ascii)?|-F|--form(-string)?|-T|--upload-file|--json)(\s|=|@|$)|\s(-X|--request)\s*['"]?(POST|PUT|PATCH))`,
	"up.wget":  `(?i)^wget\b.*--(post-(data|file)|body-(data|file)|method[= ]['"]?(POST|PUT|PATCH))`,
	"up.ps":    `(?i)^(Invoke-WebRequest|Invoke-RestMethod|iwr|irm)\b.*\s-(Method\s+['"]?(Post|Put|Patch)|InFile|Body)\b|\bUpload(File|String|Data)\b`,
	"up.cloud": `(?i)^(aws\s+s3\s+(cp|sync|mv)\s+(-\S+\s+)*[^s\s]\S*\s+s3://|gsutil\s+(-\S+\s+)*(cp|rsync|mv)\s+(-\S+\s+)*[^g\s]\S*\s+gs://|az\s+storage\s+blob\s+upload|rclone\s+(copy|sync|move)\b)`,

	// 7: persistence paths and commands.
	"persist.path": `(?i)(^|/)(\.bashrc|\.bash_profile|\.bash_login|\.profile|\.zshrc|\.zprofile|\.zshenv|\.zlogin|\.kshrc|\.cshrc|\.tcshrc|config\.fish|microsoft\.powershell_profile\.ps1|profile\.ps1)$|^/etc/(profile|profile\.d/|bash\.bashrc|zshrc|environment|cron|crontab|anacrontab|systemd/|rc\.local|init\.d/|xdg/autostart/)|^/var/spool/cron/|/systemd/user/|/library/launch(agents|daemons)/|/start menu/programs/startup/|/\.config/autostart/`,
	"persist.cmd":  `(?i)(^|[\s;&|(])(crontab\s+(-[a-km-z]*\s+)*[^-\s]|crontab\s+-e\b|crontab\s+-\s*$|systemctl\s+(--user\s+)?(enable|link)\b|launchctl\s+(load|bootstrap|enable|submit)\b|schtasks(\.exe)?\s+/create\b|Register-ScheduledTask\b|New-ScheduledTask\b|New-Service\b|sc(\.exe)?\s+create\b|reg(\.exe)?\s+add\s+\S*\\(Run|RunOnce)\b|(New|Set)-ItemProperty\b.*\\CurrentVersion\\(Run|RunOnce)\b|\bat\s+(now|[0-9]))`,

	// 8: destructive commands beyond rm.
	"destroy.disk":  `(?i)(^|[\s;&|(])(dd\s+[^|;&]*\bof=/dev/|mkfs(\.\w+)?\s|wipefs\s|shred\s+[^|;&]*/dev/|format(\.com)?\s+[a-z]:|diskpart\b|Clear-Disk\b|Format-Volume\b|Remove-Partition\b)`,
	"destroy.cloud": `(?i)(^|[\s;&|(])(aws\s+s3\s+(rm|rb)\b|aws\s+\S+\s+(delete|terminate|remove|deregister)-\S+|gcloud\s+.*\s(delete|remove)\b|az\s+.*\s(delete|purge)\b|kubectl\s+delete\b|terraform\s+destroy\b|terraform\s+apply\b.*-destroy\b|pulumi\s+destroy\b|gsutil\s+(-\S+\s+)*(rm|rb)\b|doctl\s+.*\sdelete\b|heroku\s+apps:destroy\b)`,
	"destroy.other": `(?i)(^|[\s;&|(])(find\s+[^|;&]*\s(-delete|-exec\s+rm)\b|git\s+(-C\s+\S+\s+)*clean\s+-[a-z]*f|shred\s|truncate\s+-s\s*0\s)|\b(drop\s+(table|database|schema)|truncate\s+table)\b`,

	// 10: privilege escalation.
	"priv.high": `(?i)\bchmod\s+(-\S+\s+)*([ugoa]*\+[rwxt]*s|0?[2-7][0-7]{3})\b|\bsetcap\b|\bvisudo\b|NOPASSWD|\busermod\s+.*-a?G\s*\S*\b(sudo|wheel|admin|root|docker)\b|\bgpasswd\s+-a\s+\S+\s+(sudo|wheel|admin|docker)\b|\badduser\s+\S+\s+(sudo|wheel|admin)\b|\bnet(\.exe)?\s+localgroup\s+administrators\b.*\s/add\b|\bAdd-LocalGroupMember\b.*\bAdministrators\b|\bdseditgroup\b.*\badmin\b`,
	"priv.path": `(?i)^/etc/(sudoers|sudoers\.d/|passwd$|group$|shadow$)`,
	"priv.med":  `(?i)\bchmod\s+(-\S+\s+)*(0?777|0?666|a\+rwx|[ugoa]*o\+w|a\+w)\b|\bchown\s|\bicacls\b.*(Everyone|\*S-1-1-0|Users):\(?[^)]*F|\btakeown\b|\bStart-Process\b.*-Verb\s+RunAs\b`,

	// 11: monitoring tamper.
	"tamper.path": `(?i)(^|/)\.claude/(settings(\.local)?\.json|hooks/|projects/)|managed-settings\.json$|/claudecode/|/\.github/hooks/|^\.github/hooks/|/\.copilot/|/\.local/share/afr(/|$)|/appdata/local/afr(/|$)|/programdata/afr(/|$)|^/var/lib/afr(/|$)|^/etc/audit/|^/var/log/|/\.bash_history$|/\.zsh_history$|consolehost_history\.txt$`,
	"tamper.cmd":  `(?i)(^|[\s;&|(])((pkill|killall)\s+(-\S+\s+)*afr\b|taskkill\b.*\bafr(\.exe)?\b|Stop-Process\b.*\bafr\b|history\s+-c\b|unset\s+HISTFILE\b|HISTFILE=/dev/null|Clear-History\b|wevtutil(\.exe)?\s+(cl|clear-log)\b|Clear-EventLog\b|auditctl\s+(-D|-e\s*0)\b|systemctl\s+(stop|disable|mask)\s+auditd\b|journalctl\s+.*--vacuum|Set-PSReadLineOption\b.*HistorySaveStyle\s+SaveNothing)|disableAllHooks`,

	// 12: permission bypass by the agent.
	"bypass":         `(?i)dangerously-skip-permissions|bypassPermissions|dangerously-bypass-approvals-and-sandbox|\bcopilot\b.*\s--(allow-all-tools|allow-all|yolo)\b|\bgemini\b.*\s(--yolo|-y\b|--approval-mode[= ]yolo)|\bcodex\b.*\s--yolo\b`,
	"bypass.content": `(?i)"(defaultMode|allow|additionalDirectories)"\s*:|bypassPermissions|dangerously-skip-permissions`,
	"bypass.path":    `(?i)(^|/)\.claude/settings(\.local)?\.json$|managed-settings\.json$|/\.copilot/|/\.codex/config|/\.gemini/settings\.json$`,

	// 13: environment dump, matched per simple command.
	"env.cmd": `(?i)^(env(\s+-0)?|printenv(\s+-0)?|export\s+-p|(declare|typeset)\s+-[a-z]*[px][a-z]*|set|Get-ChildItem\s+env:\\?\*?|(gci|ls|dir|gi)\s+env:\\?\*?)\s*$`,
	"env.any": `(?i)/proc/(self|[0-9]+|\$\$|\*)/environ|\[(System\.)?Environment\]::GetEnvironmentVariables\(\s*\)|print\(\s*(dict\()?os\.environ\s*\)?\)|json\.dumps\(\s*(dict\()?os\.environ|\bfor\s+\w+\s+in\s+os\.environ\b|console\.log\(\s*process\.env\s*\)|JSON\.stringify\(\s*process\.env\b`,

	// 14: reverse shells and tunnels.
	"rs.high": `(?i)/dev/(tcp|udp)/|\b(nc|ncat|netcat)\b[^|;&]*\s(-e|-c|--exec|--sh-exec|--lua-exec)\s|\bmkfifo\b.*\b(nc|ncat|netcat|telnet|openssl)\b|\bsocat\b.*\b(exec|system):|\b(ba)?sh\s+-i\s*[<>]&|\bsocket\b.*\b(dup2|pty\.spawn|subprocess|fsockopen|/bin/(ba)?sh)\b|\bfsockopen\b|\bTCPSocket\b.*\b(exec|spawn|/bin/sh)\b|Net\.Sockets\.TCPClient\b.*\bGetStream\b|\bngrok\b|\bchisel\b|\bfrpc\b|\blocaltunnel\b|\blt\s+(--port|-p)\b|serveo\.net|localhost\.run|\bbore\s+local\b|pinggy\.io|\btelebit\b`,
	"rs.med":  `(?i)\bcloudflared\b.*\btunnel\b|\bcode(-insiders)?(\.exe)?\s+tunnel\b|\bdevtunnel\b|\bssh\b[^|;&]*\s-[A-Za-z]*[RD]\b|\btailscale\s+funnel\b|\bplayit\b`,

	// 16: scripts run with the execution policy bypassed.
	"policy.bypass": `(?i)(^|[\s;|&(])-(ep|ex[a-z]*)(\s+|:)['"]?(bypass|unrestricted)\b|\bset-executionpolicy\b.*\b(bypass|unrestricted)\b`,

	// 15: install from a URL, matched per simple command.
	"install": `(?i)^((pip[0-9.]*|python[0-9.]*\s+-m\s+pip|uv\s+pip|pipx|uv\s+tool)\s+install\b.*(\s(git\+|svn\+|hg\+|https?://)|\s(-i|--index-url|--extra-index-url|--trusted-host|-f|--find-links)(\s|=))|(npm|pnpm|yarn|bun)\s+(install|i|add)\b.*\s(git\+|git://|https?://|github:|gitlab:|bitbucket:|--registry\b|[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9_.-]+(#\S*)?(\s|$))|gem\s+install\b.*(--source|https?://)|cargo\s+install\b.*\s--(git|registry|index)\b|brew\s+(install|tap)\b.*https?://|msiexec(\.exe)?\b.*/(i|package)\s+["']?https?://|Install-Package\b.*-Source\s+["']?https?://)`,

	// Paths not worth flagging as "outside the workspace".
	"outside.ok": `(?i)^/(proc|dev)/|(^|/)(\.cache|\.npm|\.cargo/registry|go/pkg/mod|\.m2|\.gradle|library/caches|\.local/share/pnpm|appdata/local/(npm-cache|pip/cache|yarn/cache))(/|$)`,
}

var (
	reMu sync.Mutex
	re   = map[string]*regexp.Regexp{}
)

// get compiles a pattern on first use, so a hook call compiles only what
// its event needs. A bad pattern panics here, inside the rule's recover,
// and costs that rule's highlights.
func get(name string) *regexp.Regexp {
	reMu.Lock()
	defer reMu.Unlock()
	if r, ok := re[name]; ok {
		return r
	}
	p, ok := patterns[name]
	if !ok {
		panic("rules: no pattern " + name)
	}
	r := regexp.MustCompile(p)
	re[name] = r
	return r
}

func match(name, s string) bool { return get(name).MatchString(s) }

func find(name, s string) string { return get(name).FindString(s) }

type toolRule struct {
	name string
	f    func(in input, c *Context, s highlightSet)
}

// toolRules run on every tool call. Secrets (rule 2) run separately on
// every kind of content.
var toolRules = []toolRule{
	{CredentialFile, credentialRule},
	{DownloadPipeShell, downloadRule},
	{DecodeAndRun, decodeRule},
	{OutboundTransfer, transferRule},
	{WriteOutside, outsideRule},
	{Autostart, autostartRule},
	{Destructive, destructiveRule},
	{HistoryRewrite, gitRule},
	{PrivEscalation, privRule},
	{MonitoringTamper, tamperRule},
	{PermissionBypass, bypassRule},
	{EnvDump, envRule},
	{ReverseShell, reverseShellRule},
	{InstallFromURL, installRule},
	{ScriptBypass, scriptRule},
}

// cmdText is the command with backslashes as slashes, so path patterns
// match Windows paths in commands too.
func cmdText(in input) string { return strings.ReplaceAll(in.command, `\`, "/") }

func credentialRule(in input, c *Context, s highlightSet) {
	check := func(text string) {
		for _, m := range get("cred.host").FindAllString(text, -1) {
			m = strings.Trim(m, " \t\"'=:")
			if strings.Contains(strings.ToLower(m), ".ssh") && match("cred.ssh.ok", m) {
				continue // known_hosts, config, public keys
			}
			s.add(CredentialFile, High, m)
			return
		}
		if m := find("cred.env", text); m != "" && !match("cred.env.ok", text) {
			s.add(CredentialFile, Medium, strings.Trim(m, " \t\"'=:;|&)"))
		}
	}
	for _, p := range in.reads {
		check(p)
	}
	if in.command != "" {
		check(cmdText(in))
		if m := find("cred.cmd", in.command); m != "" {
			s.add(CredentialFile, High, m)
		}
	}
}

func secretsIn(text string, s highlightSet) {
	if text == "" {
		return
	}
	if m := find("secret", text); m != "" {
		s.add(SecretInContent, Medium, secretKind(m)+" "+mask(m))
	}
}

func secretKind(m string) string {
	switch {
	case strings.HasPrefix(m, "AKIA"), strings.HasPrefix(m, "ASIA"):
		return "AWS access key"
	case strings.HasPrefix(m, "gh"), strings.HasPrefix(m, "github_pat_"):
		return "GitHub token"
	case strings.HasPrefix(m, "glpat-"):
		return "GitLab token"
	case strings.HasPrefix(m, "xox"), strings.HasPrefix(m, "hooks.slack.com"):
		return "Slack token"
	case strings.HasPrefix(m, "-----BEGIN"):
		return "private key"
	case strings.HasPrefix(m, "AIza"):
		return "Google API key"
	case strings.HasPrefix(m, "sk_live_"), strings.HasPrefix(m, "rk_live_"):
		return "Stripe key"
	case strings.HasPrefix(m, "sk-ant-"):
		return "Anthropic API key"
	case strings.HasPrefix(m, "sk-"):
		return "OpenAI-style API key"
	case strings.HasPrefix(m, "npm_"):
		return "npm token"
	}
	return "secret"
}

// mask keeps a secret recognizable without copying it into the detail.
func mask(m string) string {
	if strings.HasPrefix(m, "-----BEGIN") {
		return ""
	}
	if len(m) <= 12 {
		return strings.Repeat("*", len(m))
	}
	return m[:4] + "..." + m[len(m)-4:]
}

func downloadRule(in input, c *Context, s highlightSet) {
	if in.command == "" {
		return
	}
	for _, name := range []string{"dl.pipe", "dl.subst", "dl.iex"} {
		if m := find(name, in.command); m != "" {
			s.add(DownloadPipeShell, High, m)
			return
		}
	}
}

func decodeRule(in input, c *Context, s highlightSet) {
	if in.command == "" {
		return
	}
	for _, name := range []string{"dec.pipe", "dec.eval"} {
		if m := find(name, in.command); m != "" {
			s.add(DecodeAndRun, High, m)
			return
		}
	}
	if m := find("dec.bare", in.command); m != "" {
		s.add(DecodeAndRun, Medium, m)
	}
}

func transferRule(in input, c *Context, s highlightSet) {
	for _, seg := range in.segs {
		cmd, args := seg.name()
		text := cmd + " " + strings.Join(args, " ")
		hit := match("up.curl", text) || match("up.wget", text) || match("up.ps", text) || match("up.cloud", text)
		switch cmd {
		case "scp", "rsync":
			if pos := positional(args); len(pos) >= 2 && isRemote(pos[len(pos)-1]) {
				hit = true
			}
		case "sftp", "ftp", "lftp":
			hit = hit || strings.Contains(strings.ToLower(seg.raw), "put ")
		case "nc", "ncat", "netcat", "socat", "telnet":
			hit = hit || seg.piped || len(seg.inputs) > 0
		}
		if !hit {
			continue
		}
		if m := find("net.share", seg.raw); m != "" {
			s.add(OutboundTransfer, High, "to "+m+": "+seg.raw)
			return
		}
		s.add(OutboundTransfer, Medium, seg.raw)
	}
}

// isRemote reports an scp or rsync destination like host:path or
// user@host:path (not a Windows drive like C:\).
func isRemote(a string) bool {
	i := strings.Index(a, ":")
	return i > 1 && !strings.ContainsAny(a[:i], `/\`) && !strings.HasPrefix(a, "-")
}

func outsideRule(in input, c *Context, s highlightSet) {
	ws := normPath(c.Workspace, "", c)
	if ws == "" {
		return
	}
	for _, p := range in.writes {
		if !isAbs(p) || inWorkspace(p, c) || match("outside.ok", p) {
			continue
		}
		if isTemp(p, c) {
			s.add(WriteOutside, Low, in.show(p))
			continue
		}
		s.add(WriteOutside, Medium, in.show(p))
	}
}

// inWorkspace reports a path under the workspace or any folder that
// counts as part of it.
func inWorkspace(p string, c *Context) bool {
	for _, w := range append([]string{c.Workspace}, c.Workspaces...) {
		if w := normPath(w, "", c); w != "" && under(p, w) {
			return true
		}
	}
	return false
}

func isTemp(p string, c *Context) bool {
	for _, t := range []string{"/tmp", "/var/tmp", "/private/tmp", "/private/var/folders", "c:/windows/temp"} {
		if under(p, t) {
			return true
		}
	}
	for _, t := range c.TempDirs {
		if under(p, normPath(t, "", c)) {
			return true
		}
	}
	// %LOCALAPPDATA%\Temp for any user.
	if strings.HasPrefix(p, "c:/users/") || hasDrive(p) {
		parts := strings.Split(p, "/")
		if len(parts) >= 6 && parts[1] == "users" && parts[3] == "appdata" && parts[4] == "local" && parts[5] == "temp" {
			return true
		}
	}
	return false
}

func autostartRule(in input, c *Context, s highlightSet) {
	for _, p := range in.writes {
		if match("persist.path", p) {
			s.add(Autostart, High, in.show(p))
			return
		}
	}
	if in.command != "" {
		if m := find("persist.cmd", in.command); m != "" {
			s.add(Autostart, High, strings.TrimLeft(m, " ;&|("))
		}
	}
}

func destructiveRule(in input, c *Context, s highlightSet) {
	if in.command == "" {
		return
	}
	text := cmdText(in)
	if m := find("destroy.disk", text); m != "" {
		s.add(Destructive, High, strings.TrimLeft(m, " ;&|("))
		return
	}
	if m := find("destroy.cloud", in.command); m != "" {
		s.add(Destructive, High, strings.TrimLeft(m, " ;&|("))
		return
	}
	for _, seg := range in.segs {
		cmd, args := seg.name()
		if !removes(cmd, args) {
			continue
		}
		sev := Medium
		if slices.Contains(args, "--no-preserve-root") {
			sev = High
		}
		for _, t := range deleteTargets(args) {
			if criticalPath(t, c) {
				sev = High
			}
		}
		s.add(Destructive, sev, seg.raw)
		if sev == High {
			return
		}
	}
	if m := find("destroy.other", in.command); m != "" {
		s.add(Destructive, Medium, strings.TrimLeft(m, " ;&|("))
	}
}

// removes reports a recursive or forced delete: rm -r/-f, Remove-Item
// -Recurse/-Force, rd /s, del /s or /q.
func removes(cmd string, args []string) bool {
	switch cmd {
	case "rm", "remove-item", "ri", "del", "erase", "rd", "rmdir":
	default:
		return false
	}
	for _, a := range args {
		l := strings.ToLower(a)
		switch {
		case l == "--recursive" || l == "--force" || l == "-recurse" || l == "-force" || l == "/s" || l == "/q":
			return true
		case strings.HasPrefix(l, "-") && !strings.HasPrefix(l, "--") && len(l) <= 4 && strings.ContainsAny(l[1:], "rf"):
			return true
		}
	}
	return false
}

// deleteTargets lists what a delete names, skipping flags and cmd.exe
// switches such as /s and /q.
func deleteTargets(args []string) []string {
	var out []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") || (len(a) == 2 && a[0] == '/') {
			continue
		}
		out = append(out, a)
	}
	return out
}

// criticalPath is the root, a home folder itself, or a system folder.
func criticalPath(t string, c *Context) bool {
	raw := strings.ReplaceAll(strings.Trim(t, `"'`), `\`, "/")
	raw = strings.TrimSuffix(strings.TrimSuffix(raw, "/*"), "/")
	switch raw {
	case "", "~", "$HOME", "${HOME}", "%USERPROFILE%", "$env:USERPROFILE":
		return true
	}
	p := normPath(raw, "", c)
	if !isAbs(p) {
		return false
	}
	if p == "/" || (hasDrive(p) && len(p) <= 3) {
		return true
	}
	if home := normPath(c.Home, "", c); home != "" && p == home {
		return true
	}
	if under(p, "/tmp") || under(p, "/var/tmp") || under(p, "/private/tmp") {
		return false
	}
	// A folder that holds home folders, or one user's home folder.
	for _, d := range []string{"/home", "/users", "c:/users"} {
		if under(p, d) && strings.Count(strings.TrimPrefix(p, d), "/") <= 1 {
			return true
		}
	}
	for _, d := range []string{"/etc", "/usr", "/bin", "/sbin", "/lib", "/lib64", "/boot", "/var", "/opt", "/root",
		"/sys", "/proc", "/dev", "/system", "/library", "/applications", "/private/etc", "/private/var",
		"c:/windows", "c:/program files", "c:/program files (x86)", "c:/programdata"} {
		if under(p, d) {
			return true
		}
	}
	return false
}

func gitRule(in input, c *Context, s highlightSet) {
	for _, seg := range in.segs {
		cmd, args := seg.name()
		if cmd != "git" {
			continue
		}
		// Skip global options: -C dir, -c key=value, --git-dir=...
		for len(args) > 0 && strings.HasPrefix(args[0], "-") {
			takesArg := args[0] == "-C" || args[0] == "-c"
			args = args[1:]
			if takesArg && len(args) > 0 {
				args = args[1:]
			}
		}
		if len(args) == 0 {
			continue
		}
		sub, rest := args[0], args[1:]
		hit := false
		switch sub {
		case "push":
			hit = hasLong(rest, "--force") || hasLong(rest, "--force-with-lease") || hasLong(rest, "--force-if-includes") ||
				hasLong(rest, "--mirror") || hasLong(rest, "--delete") || hasShort(rest, 'f') || hasShort(rest, 'd')
			for _, a := range positional(rest) {
				hit = hit || strings.HasPrefix(a, "+") || strings.HasPrefix(a, ":")
			}
		case "reset":
			hit = slices.Contains(rest, "--hard")
		case "filter-branch", "filter-repo":
			hit = true
		case "reflog":
			hit = len(rest) > 0 && (rest[0] == "expire" || rest[0] == "delete")
		case "branch":
			hit = hasShort(rest, 'D') || (hasLong(rest, "--delete") && hasLong(rest, "--force"))
		case "update-ref":
			hit = slices.Contains(rest, "-d")
		case "gc":
			hit = slices.Contains(rest, "--prune=now") || slices.Contains(rest, "--prune=all")
		case "stash":
			hit = len(rest) > 0 && rest[0] == "clear"
		}
		if hit {
			s.add(HistoryRewrite, Medium, seg.raw)
			return
		}
	}
}

// hasShort reports a single-letter flag, alone or in a group (-f, -fu).
func hasShort(args []string, letter byte) bool {
	for _, a := range args {
		if len(a) >= 2 && a[0] == '-' && a[1] != '-' && strings.IndexByte(a[1:], letter) >= 0 {
			return true
		}
	}
	return false
}

func privRule(in input, c *Context, s highlightSet) {
	for _, p := range in.writes {
		if match("priv.path", p) {
			s.add(PrivEscalation, High, in.show(p))
			return
		}
	}
	if in.command == "" {
		return
	}
	if m := find("priv.high", in.command); m != "" {
		s.add(PrivEscalation, High, m)
		return
	}
	for _, seg := range in.segs {
		switch seg.first() {
		case "sudo", "su", "doas", "pkexec", "runas", "gsudo":
			s.add(PrivEscalation, Medium, seg.raw)
			return
		}
	}
	if m := find("priv.med", in.command); m != "" {
		s.add(PrivEscalation, Medium, m)
	}
}

func tamperRule(in input, c *Context, s highlightSet) {
	data := normPath(c.DataDir, "", c)
	touched := append([]string(nil), in.writes...)
	for _, seg := range in.segs {
		cmd, args := seg.name()
		if removes(cmd, args) || cmd == "rm" || cmd == "unlink" || cmd == "shred" || cmd == "truncate" ||
			cmd == "remove-item" || cmd == "del" {
			for _, t := range deleteTargets(args) {
				touched = append(touched, normPath(t, "", c))
			}
		}
	}
	for _, p := range touched {
		if match("tamper.path", p) || (data != "" && under(p, data)) {
			s.add(MonitoringTamper, High, in.show(p))
			return
		}
	}
	if in.command != "" {
		if m := find("tamper.cmd", in.command); m != "" {
			s.add(MonitoringTamper, High, strings.TrimLeft(m, " ;&|("))
			return
		}
	}
	if strings.Contains(in.content, "disableAllHooks") {
		s.add(MonitoringTamper, High, "disableAllHooks")
	}
}

func bypassRule(in input, c *Context, s highlightSet) {
	if in.command != "" {
		if m := find("bypass", in.command); m != "" {
			s.add(PermissionBypass, High, m)
			return
		}
	}
	if !in.byTool {
		return
	}
	for _, p := range in.writes {
		if match("bypass.path", p) && match("bypass.content", in.content) {
			s.add(PermissionBypass, High, "permission settings edited: "+in.show(p))
			return
		}
	}
}

func envRule(in input, c *Context, s highlightSet) {
	for _, seg := range in.segs {
		cmd, args := seg.name()
		if cmd == "env" && len(args) > 0 && args[0] != "-0" {
			continue // env VAR=x cmd, or env cmd: running, not dumping
		}
		if match("env.cmd", strings.TrimSpace(cmd+" "+strings.Join(args, " "))) {
			s.add(EnvDump, Medium, seg.raw)
			return
		}
	}
	if in.command != "" {
		if m := find("env.any", in.command); m != "" {
			s.add(EnvDump, Medium, m)
		}
	}
}

func reverseShellRule(in input, c *Context, s highlightSet) {
	if in.command == "" {
		return
	}
	if m := find("rs.high", in.command); m != "" {
		s.add(ReverseShell, High, m)
		return
	}
	if m := find("rs.med", in.command); m != "" {
		s.add(ReverseShell, Medium, m)
	}
}

func installRule(in input, c *Context, s highlightSet) {
	for _, seg := range in.segs {
		cmd, args := seg.name()
		if match("install", cmd+" "+strings.Join(args, " ")) {
			s.add(InstallFromURL, Medium, seg.raw)
			return
		}
	}
}

// MaskSecrets replaces every secret-shaped string in text with its masked
// form, for summary lines a reviewer may share. It never panics.
func MaskSecrets(text string) (out string) {
	out = text
	defer func() {
		if recover() != nil {
			out = text
		}
	}()
	return get("secret").ReplaceAllStringFunc(text, func(m string) string {
		if v := mask(m); v != "" {
			return v
		}
		return "[private key]"
	})
}

// Interpreters that run a script file named by their first argument.
var scriptRunners = map[string]bool{
	"bash": true, "sh": true, "zsh": true, "dash": true, "ksh": true, "python": true, "python3": true,
	"node": true, "perl": true, "ruby": true, "cscript": true, "wscript": true, "cmd": true,
}

// Script file extensions.
var scriptExts = []string{".ps1", ".psm1", ".bat", ".cmd", ".vbs", ".js", ".sh", ".py", ".pl", ".rb"}

// scriptRule flags a script run with PowerShell's execution policy
// bypassed, and a script run from a temp folder outside the workspace and
// the session's scratchpad: writing a script somewhere disposable and
// running it is a common way to slip past review (ATT&CK T1059.001).
func scriptRule(in input, c *Context, s highlightSet) {
	if in.command == "" {
		return
	}
	if m := find("policy.bypass", in.command); m != "" {
		s.add(ScriptBypass, Medium, "execution policy bypass: "+strings.TrimLeft(m, " ;&|("))
	}
	for _, seg := range in.segs {
		for _, t := range seg.scriptTargets() {
			p := in.path(t, "", c)
			if isAbs(p) && isTemp(p, c) && !inWorkspace(p, c) {
				s.add(ScriptBypass, Medium, "script run from a temp folder: "+in.show(p))
				return
			}
		}
	}
}

// scriptTargets lists script files a segment runs: powershell -File X,
// an interpreter's first argument, or the command word itself.
func (s segment) scriptTargets() []string {
	if len(s.words) == 0 {
		return nil
	}
	isScript := func(w string) bool {
		l := strings.ToLower(strings.Trim(w, `"'`))
		for _, e := range scriptExts {
			if strings.HasSuffix(l, e) {
				return true
			}
		}
		return false
	}
	cmd, args := s.name()
	var out []string
	switch {
	case cmd == "powershell" || cmd == "pwsh":
		if v := flagValue(args, "-File", "-f"); v != "" {
			out = append(out, v)
		}
	case scriptRunners[cmd]:
		if pos := positional(args); len(pos) > 0 && isScript(pos[0]) {
			out = append(out, pos[0])
		}
	case isScript(s.words[0]):
		out = append(out, s.words[0])
	}
	return out
}
