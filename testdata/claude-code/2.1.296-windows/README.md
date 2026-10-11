Payloads from the first native Windows capture (Claude Code 2.1.296, interactive CLI,
`permission_mode: auto`, PowerShell tool, hooks in exec form). The user name is replaced
with `user`. `PreToolUse-PowerShell-denied.json` is a call auto mode denied: no
`PostToolUse` followed (see docs/findings.md F-004). The two `SubagentStop` files are
Claude Code's own background tasks, a prompt suggestion and a session recap (F-003).
