# Highlight rules

afr tags recorded events with highlights so a reviewer knows where to look first. A
highlight is a review priority, never a decision: afr does not block, delay, or change
anything the agent does. The rules live in `internal/rules`.

Every rule maps to a published technique or guideline. The severities are afr's own,
because no framework assigns severity to these behaviors; they were benchmarked against
the Sigma, Elastic, and Splunk detection rule sets.

## Severity

| Severity | Meaning | Compare |
| --- | --- | --- |
| high | Review promptly | Sigma "high": prompt review |
| medium | Review in the normal pass | Sigma "medium": review frequently |
| low | Worth seeing, rarely urgent | Used only for writes to temp folders |

Rules marked **afr-adjusted** below sit above vendor norms on purpose: the actor is an AI
agent reviewed one session at a time, where a false positive is cheap, rather than a human
admin in fleet telemetry.

## When rules run

- **At write time.** `afr hook` evaluates each event as it is recorded, including tool
  calls recovered from the transcript that no hook saw. Highlights are stored in the event
  and covered by the hash chain. Each evaluated event records `rules_version`.
- **At read time.** `afr replay` re-runs the current rules over events recorded by an
  older rules version (or before rules existed) and shows what they would add, marked
  with `*`. Stored highlights stay the record; the log is never changed. Events whose
  content was purged are not re-evaluated.

Bump `rules.Version` whenever a rule's matching or severity changes.

## Rules

| # | Rule id | Flags | Severity | Grounding |
| --- | --- | --- | --- | --- |
| 1 | `secrets.read_credential_file` | Reads of host credentials: SSH private keys, `~/.aws/credentials`, kube and gcloud config, `.netrc`, `.git-credentials`, keychains, password stores, Windows credential stores | high | ATT&CK [T1552.001](https://attack.mitre.org/techniques/T1552/001/), [T1552.004](https://attack.mitre.org/techniques/T1552/004/); campaign [C0062](https://attack.mitre.org/campaigns/C0062/) (Claude Code) |
| | | Project `.env` files (not `.env.example`) | medium | Claude Code allows `.env` reads by default; host credentials it blocks |
| 2 | `secrets.in_content` | Secret-shaped strings (AWS, GitHub, GitLab, Slack, Stripe, Google, OpenAI, Anthropic, npm keys; private key blocks) in commands, output, prompts, or replies. The detail is masked. | medium | NIST SP 800-218A PO.5.1 ("monitor for plaintext secrets"); OWASP LLM02 |
| 3 | `exec.download_pipe_shell` | `curl` or `wget` piped to a shell or interpreter, `bash -c "$(curl ...)"`, `iwr \| iex` | high | ATT&CK [T1105](https://attack.mitre.org/techniques/T1105/), T1059.004; NIST CAISI agent hijacking task |
| 4 | `exec.decode_and_run` | Decoded content fed to an interpreter (`base64 -d \| sh`, `exec(b64decode(...))`, `iex` with `FromBase64String`) | high | ATT&CK [T1140](https://attack.mitre.org/techniques/T1140/), [T1027.010](https://attack.mitre.org/techniques/T1027/010/); CISA LOTL guidance |
| | | A bare encoded command (`powershell -enc`, `FromBase64String`) | medium | Sigma ladders generic `-enc` at medium |
| 5 | `net.outbound_transfer` | Uploads: `curl -d/-F/-T`, POST or PUT, `wget --post-file`, `scp`/`rsync` to a remote, data piped to `nc`, PowerShell `-Body`/`-InFile`, cloud storage uploads | medium | ATT&CK [T1048](https://attack.mitre.org/techniques/T1048/), [T1567](https://attack.mitre.org/techniques/T1567/) |
| | | The same, to file-sharing, paste, or webhook sites (0x0.st, transfer.sh, pastebin, webhook.site, Discord and Slack webhooks, ...) | high | Sigma rates uploads to file-sharing sites high |
| 6 | `fs.write_outside_workspace` | Writes outside the project folder (file tools, redirects, `tee`, `cp`, `mv`, PowerShell content cmdlets) | medium (afr-adjusted) | OWASP ASI05 mitigation 4 (dedicated working directory) |
| | | Writes to temp folders (`/tmp`, `$TMPDIR`, `%TEMP%`) | low | Caches and package stores are not flagged |
| 7 | `persist.autostart` | Shell rc and profile files, PowerShell profiles, cron, systemd units and `systemctl enable`, launchd, Run keys, scheduled tasks, new services | high (afr-adjusted) | ATT&CK [T1546.004](https://attack.mitre.org/techniques/T1546/004/), T1053.003, T1543, T1547.001; Elastic GenAI rc-write rule (high) |
| 8 | `fs.destructive_command` | `rm -r/-f`, `Remove-Item -Recurse`, `rd /s` in the workspace; `find -delete`, `git clean -f`, `DROP TABLE` | medium (afr-adjusted) | ATT&CK [T1485](https://attack.mitre.org/techniques/T1485/) (names `rm`) |
| | | Targets `/`, a home folder, or system folders; `dd` or `mkfs` on devices; cloud resource deletion (`terraform destroy`, `aws ... delete-*`, `kubectl delete`) | high | OWASP ASI02 and ASI05 (`rm -rf /`) |
| 9 | `git.history_rewrite` | Force push, `+refspec`, remote branch delete, `reset --hard`, `filter-branch`/`filter-repo`, `reflog expire`, `branch -D`, `stash clear` | medium | Claude Code blocks force push and `reset --hard` by default; no ATT&CK technique |
| 10 | `priv.escalation` | setuid/setgid (`chmod u+s`, `chmod 4755`), `setcap`, sudoers or `NOPASSWD` edits, adding users to admin groups | high | ATT&CK [T1548](https://attack.mitre.org/techniques/T1548/); NIST SP 800-53 AC-6(9) |
| | | `sudo`, `su`, `doas`, `runas`, `chmod 777`, `chown`, `icacls ... Everyone:F` | medium (afr-adjusted) | ATT&CK [T1222.002](https://attack.mitre.org/techniques/T1222/002/) |
| 11 | `agent.monitoring_tamper` | Writes or deletes of agent hook settings (`.claude/settings*.json`, `.github/hooks`, `~/.copilot`), transcripts, afr's data, audit config, or shell history; killing afr; clearing logs; `disableAllHooks` | high | ATT&CK [T1685](https://attack.mitre.org/techniques/T1685/), software S9043; ATLAS AML.T0081; NIST AU-9 |
| 12 | `agent.permission_bypass` | The agent launches agents with bypass flags (`--dangerously-skip-permissions`, `--yolo`) or edits its own permission settings | high | ATLAS AML.T0081, AML.T0103; Five Eyes "Prohibit agents from modifying their own privileges" |
| | | A session runs in `bypassPermissions` mode (flagged once per change) | medium | Elastic and Splunk rate human-launched bypass medium or lower |
| 13 | `secrets.env_dump` | Dumping all environment variables: `env`, `printenv`, `set`, `export -p`, `/proc/*/environ`, `Get-ChildItem env:` | medium (afr-adjusted) | ATLAS AML.T0055 (names environment variables) |
| 14 | `net.reverse_shell_or_tunnel` | Reverse shells (`/dev/tcp`, `nc -e`, socket one-liners), ngrok-class tunnels, chisel | high | ATT&CK T1059.004, T1095, [T1572](https://attack.mitre.org/techniques/T1572/), ngrok S0508 |
| | | `cloudflared tunnel`, VS Code and dev tunnels, `ssh -R` and `ssh -D`, Tailscale Funnel | medium | Vendors agree these have common legitimate use |
| 15 | `exec.install_from_url` | Package installs from a URL, git repo, or custom index (`pip install git+...`, `-i`, `npm install github:...`) | medium | ATT&CK [T1195.001](https://attack.mitre.org/techniques/T1195/001/); ATLAS AML.T0060 |

Sources for the mappings beyond the links: [MITRE ATLAS](https://atlas.mitre.org/),
[OWASP Top 10 for Agentic Applications 2026](https://genai.owasp.org/),
[NIST SP 800-53 Rev. 5](https://csrc.nist.gov/pubs/sp/800/53/r5/upd1/final),
[NIST SP 800-218A](https://nvlpubs.nist.gov/nistpubs/SpecialPublications/NIST.SP.800-218A.pdf),
the Five Eyes guide [Careful adoption of agentic AI services](https://media.defense.gov/2026/Apr/30/2003922823/-1/-1/0/CAREFUL%20ADOPTION%20OF%20AGENTIC%20AI%20SERVICES_FINAL.PDF),
[Claude Code permission modes](https://code.claude.com/docs/en/permission-modes), and the
[Sigma rule specification](https://github.com/SigmaHQ/sigma-specification).

## Deliberately not flagged

These are common in normal coding work, and flagging them would bury the signal:
plain `rm file`, `git rebase`, `git commit --amend`, `ssh -L`, `printenv VAR`,
`base64 -d` without an interpreter, `npm install @scope/pkg`, `pip install pkg`,
reading `known_hosts` or public keys, and writes to caches and package stores.

## Limits

- Rules match command text and paths. Obfuscated or indirect commands (a script file that
  does the work, a renamed binary) can evade them. Rules aid review; they are not
  detection guarantees.
- Rules see content after the 64 KB cap, so a secret past the cap in a long output is
  missed.
- A relative path in a shell command is taken as relative to the session's working
  directory; a `cd` earlier in the same command is not followed.
- Rules 2 and 13 make afr's own log sensitive when they fire: the secret is in the
  recorded content (the highlight detail is masked). Encryption at rest or optional
  redaction is on the roadmap.

## Changing rules

Patterns compile on first use inside the hook's recovered path, and each rule runs under
its own recover, so a bad rule costs highlights, never the event. A unit test compiles
every pattern. Add a positive and a negative case to `internal/rules/rules_test.go` for
each change, bump `rules.Version`, and keep the hook latency gate green.
