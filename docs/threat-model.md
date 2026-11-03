# AFR threat model (v0.1 draft)

_Status: draft, updated 2026-10-10. Covers AFR v0.1: Claude Code on Linux and Windows, local logs only._

AFR is a **recorder, not a control**. It writes a complete, verifiable log of each AI agent
session so a reviewer can see what the agent was asked, what it said, and what it ran. It
never blocks, delays on purpose, or changes an agent action. Hooks are cooperative: they run
because the harness runs them, on a machine the user controls.

## What AFR protects

| Asset | Meaning |
| --- | --- |
| Completeness | Every prompt, reply, and tool call in a session is in the log, or its absence is visible. |
| Integrity | The log says what happened; later edits are detectable. |
| Confidentiality | Logs hold prompts, replies, commands, and output verbatim, with no redaction, so they contain secrets and code. |
| Non-interference | AFR never changes, blocks, or stalls what the agent does. |
| Truthful display | The replay shows the reviewer what was captured, not what captured text tries to make them see. |

## Who and what it guards against

1. **A careless agent**: destructive or wrong actions with no bad intent (`rm -rf`, force push).
2. **A misled agent**: prompt injection from files, web pages, or tool output steering it to
   read secrets, download and run code, send data out, or tamper with its own monitoring.
3. **A user hiding activity**: removing hooks, editing or deleting logs, killing AFR.
4. **Another local reader**: a different user or process reading the logs.
5. **Hostile content aimed at the reviewer**: terminal escapes or bidi characters in captured text.
6. **Bad input to AFR itself**: malformed, huge, or unexpected payloads that crash or stall a hook.

## Threats, mitigations, and highlight rules

| Threat | v0.1 mitigation | Gap (later) | Highlight rule |
| --- | --- | --- | --- |
| Agent reads credentials (`~/.ssh`, `~/.aws`, `.env`, keychains) | Every tool call logged with full input | Detection only | `secrets.read_credential_file` |
| Download piped to a shell, or decode then execute | Logged | Detection only | `exec.download_pipe_shell`, `exec.decode_and_run` |
| Data sent out (`curl -d`, `scp`, `nc`, upload URLs) | Commands logged; egress seen only as command text | No network truth (OS or proxy controls) | `net.outbound_transfer` |
| Writes outside the workspace; persistence (cron, systemd, shell rc) | Logged with paths | Detection only | `fs.write_outside_workspace`, `persist.autostart` |
| Destructive commands (`rm -rf`, `git push --force`, `git reset --hard`) | Logged | Detection only | `fs.destructive_command`, `git.history_rewrite` |
| Privilege or permission bypass (`sudo`, `chmod 777`, skip-permissions flags) | Logged | Detection only | `priv.escalation`, `agent.permission_bypass` |
| Agent edits its own hooks or AFR data (`.claude/settings*`, `.github/hooks`, `~/.copilot`, AFR data dir) | Logged | Prevention needs managed settings | `agent.monitoring_tamper` |
| Secret-shaped strings in commands or output | Kept verbatim (no redaction) | Optional redaction setting | `secrets.in_content` |
| Agent dumps all environment variables (`env`, `printenv`, `/proc/*/environ`) | Logged, output included | Detection only; the dump makes the log itself sensitive | `secrets.env_dump` |
| Reverse shell or tunnel (`/dev/tcp`, `nc -e`, ngrok, `ssh -R`) | Logged | No network truth (OS or proxy controls) | `net.reverse_shell_or_tunnel` |
| Package installed from a URL, git repo, or custom index | Logged | Detection only | `exec.install_from_url` |
| Script run with the execution policy bypassed, or from a temp folder | Logged | Detection only | `exec.script_policy_bypass` |
| Harness handled a call with no hook, or refused it | Recovered from the transcript, marked `(no hook ran)` and `tool_error` | Transcript is the only source | Structural: shown in replay |
| User removes hooks | `afr coverage` and the report compare Claude Code transcripts with AFR's logs: conversations with no log, prompts AFR missed, and turns that ended without AFR's hook (F-014). Removal reaches conversations already open | Advisory only: transcripts are not tamper-evident, the same user can edit or delete them, and they expire after about 30 days. Managed settings; central shipping (Phase 5) | Structural: coverage check |
| User or process edits or deletes the log | Per-session hash chain over metadata and `content_hash`; `afr verify` | Not tamper-proof: an owner can rewrite the whole chain. Signing and central store later | Structural: `afr verify` |
| Another local user reads logs | Owner-only files (600 on Linux) | Windows owner-only ACLs; encryption at rest | n/a |
| Terminal escapes, `\r`, or bidi overrides in captured text | Replay shows them as visible escapes | Same rule for dashboard and export | n/a |
| AFR crashes or stalls the agent | Every hook exits 0, prints nothing, recovers panics; 5 s stdin and 2 s lock deadlines; payloads it cannot store are kept raw | Windows latency unmeasured | n/a |
| Copilot CLI loads Claude Code hook settings | Recorded, but labeled `claude-code` | Detect harness from payload shape (Phase 2) | n/a |

## Not in scope

- **Preventing anything.** AFR records; it is not a sandbox. Pair it with OS, container, or
  network controls when actions must be stopped.
- **Root or administrator attackers.** They can rewrite logs, binaries, and settings.
- **Tamper-proof evidence in v0.1.** Local logs are a strong record, not sole evidence,
  until events are signed and shipped off the machine.
- **Activity outside the agent.** The user's own shell, editor, and browser are not captured.
- **Harness correctness.** AFR trusts that hook payloads and transcripts reflect what the
  harness did.

## Assumptions

- The harness runs hooks as documented (verified for Claude Code 2.1.295 on Linux only).
- OS file permissions are enforced and the clock is roughly right (events sort by time).
- The AFR binary on disk is the one that was installed (code signing is planned before team rollout).

## Residual risks to revisit

- Logs are unredacted by design: a stolen log is a stolen conversation, including secrets.
- Interactive sessions and Windows are unobserved; both may change what is captured.
- Highlight rules match command text, so obfuscated commands can evade them. Rules aid
  review; they are not detection guarantees. Rules, severities (high, medium, low), and
  their framework grounding: [highlight-rules.md](highlight-rules.md).
- Rules see content after the 64 KB cap, so a secret past the cap is not flagged.
