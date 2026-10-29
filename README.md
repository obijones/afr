# AFR: Agent Flight Recorder

**A flight recorder for AI coding assistants.** AFR quietly keeps a complete record of what
an AI agent was asked, what it answered, and every action it took on your computer, so
that someone can look back later and see exactly what happened.

It only watches and writes things down. It never blocks, slows down, or changes what the
AI does.

## Why it exists

AI coding assistants such as Claude Code no longer just suggest text. They run commands,
edit and delete files, install software, and browse the web, often many steps in a row
with little human supervision. That is useful, but it raises questions that are hard to
answer after the fact:

- What exactly did the assistant do on this machine yesterday?
- Did it read a password file, send data somewhere, or change its own settings?
- Was it misled by something it read, such as a web page or a file with hidden
  instructions?
- Is the record complete, or was monitoring switched off for part of the time?

An aircraft's flight recorder does not fly the plane; it makes sure that when something
needs explaining, the facts are there. afr does the same for AI agents.

## Who it is for

- **Teams and organizations** that let developers use AI agents and need an audit trail.
- **Security and compliance reviewers** who want to check agent activity without reading
  thousands of lines of raw logs.
- **Individual developers** who want to understand what their assistant actually did.

## What it does

| | |
| --- | --- |
| **Records every session** | Each prompt, each AI reply, and every tool the agent used (commands, file reads and writes, web requests), with the results. |
| **Makes it easy to review** | Replay a session as a readable conversation, or produce one offline web page to review many sessions at once. |
| **Points out what deserves a look** | Sixteen built-in rules highlight risky actions, such as reading credentials, downloading and running code, writing outside the project, or the agent editing its own monitoring. Organizations can add their own rules. |
| **Shows when records are missing** | A coverage check compares the assistant's own history with afr's records and lists conversations or prompts afr never saw, for example while its hooks were removed. |
| **Detects tampering** | Every log entry is chained to the one before it, so later edits to a log can be detected. |
| **Packages sessions for others** | Export sessions as one zip to review on another machine. |

## What it does not do

- **It does not prevent anything.** afr is a recorder, not a guard. Pair it with operating
  system, container, or network controls when actions must be stopped.
- **It cannot stop an administrator.** Someone with full control of the machine can remove
  or rewrite afr and its logs. afr makes gaps and edits visible; it cannot make them
  impossible.
- **It does not watch the person.** Only what the AI agent does is recorded, not the user's
  own terminal, editor, or browser.

## Privacy

The logs contain everything the agent saw and did: prompts, code, command output, and any
secrets that passed through. They are stored only on the local machine, readable only by
the user who created them (owner-only files on Linux; see [Data layout](#data-layout) for
Windows). Exports and reports contain the same information, so share them only with people
allowed to see it. Sending logs to a central server is planned, not built.

## Status

Early development, not yet a released product.

| Supported now | Planned |
| --- | --- |
| Claude Code: the command-line tool and the Claude desktop app | GitHub Copilot, then OpenCode, Codex, and Cursor |
| Linux and Windows | One-command install (`afr install`) |
| Local logs, replay, reports, exports, coverage check | Central collection of logs from many machines |

## Getting started

### 1. Build afr

afr is a single program with no dependencies. You need [Go](https://go.dev/dl/) 1.25 or
later.

```sh
make build          # Linux: bin/afr
make build-windows  # Windows: bin/afr.exe
```

### 2. Connect it to Claude Code

Claude Code runs afr through **hooks**: small commands it calls at each step of a
session. Until `afr install` exists, add them by hand to your Claude Code user settings,
`~/.claude/settings.json` (on Windows, `%USERPROFILE%\.claude\settings.json`). Merge the
`hooks` block into any settings you already have, and replace the path with where you put
afr (on Windows, for example `C:\\Users\\you\\afr\\afr.exe hook`, with doubled
backslashes):

```json
{
  "hooks": {
    "SessionStart":       [{ "hooks": [{ "type": "command", "command": "/path/to/afr hook" }] }],
    "UserPromptSubmit":   [{ "hooks": [{ "type": "command", "command": "/path/to/afr hook" }] }],
    "PreToolUse":         [{ "matcher": "*", "hooks": [{ "type": "command", "command": "/path/to/afr hook" }] }],
    "PostToolUse":        [{ "matcher": "*", "hooks": [{ "type": "command", "command": "/path/to/afr hook" }] }],
    "PostToolUseFailure": [{ "matcher": "*", "hooks": [{ "type": "command", "command": "/path/to/afr hook" }] }],
    "PermissionDenied":   [{ "matcher": "*", "hooks": [{ "type": "command", "command": "/path/to/afr hook" }] }],
    "Stop":               [{ "hooks": [{ "type": "command", "command": "/path/to/afr hook" }] }],
    "SubagentStop":       [{ "hooks": [{ "type": "command", "command": "/path/to/afr hook" }] }],
    "SessionEnd":         [{ "hooks": [{ "type": "command", "command": "/path/to/afr hook" }] }]
  }
}
```

In Claude Code, `/hooks` lists the hooks it loaded. Hooks in user settings apply to every
project, in both the command-line tool and the desktop app.

To try afr without touching your own settings, use the [sandbox](#try-it-in-a-sandbox)
instead.

### 3. Use your assistant as usual, then look at what it did

```sh
afr sessions                 # list recorded sessions, most recent first
afr replay 3394              # read one session as a conversation
afr report --since 7d        # one web page to review the last week
```

## Everyday commands

| Command | What it does |
| --- | --- |
| `afr sessions` | Lists sessions, most recent first: status, turns, tool calls, highlights by severity, app, folder, id. |
| `afr replay 3394` | Shows one session as a conversation. Any unique start of the id works. |
| `afr replay --full 3394` | The same, with complete prompts, replies, tool inputs, and outputs. |
| `afr replay -o s.txt 3394` | Writes the replay to a file (UTF-8) for sharing. |
| `afr report --since 7d` | Writes one offline HTML page to review many sessions. |
| `afr export 3394 a6ac` | Zips sessions to review on another machine. `--last 2` takes the two most recent. |
| `afr coverage --since 7d` | Lists conversations and prompts afr did not record. |
| `afr verify 3394` | Checks that a session's log has not been altered. |
| `afr rules check`, `test`, `list` | Help write and check custom highlight rules. |

`afr sessions` shows the first 8 characters of each id, which every command accepts
(`--full-ids` shows whole ids). The desktop app keeps conversations open, so a desktop
session with no end shows as `idle 3h` rather than `open`.

## Reading a replay

A replay reads top to bottom in the order things happened:

- where the session began, then each prompt a person sent, numbered as turns (t1, t2, ...);
- the AI's replies, and every tool call on one line with its result;
- highlights at the front of the line they apply to;
- how the session ended.

The header shows the agent version, the models used, and whether the log's hash chain
verifies.

Some lines are marked so they are not mistaken for ordinary activity:

| Marker | Meaning |
| --- | --- |
| `(no hook ran)` | A tool call Claude Code handled without calling afr; recovered from its own history. |
| `FAILED` error, `denied by auto mode` | The call failed, or Claude Code's auto mode refused it (with the reason). |
| `HELPER` | Claude Code's own background tasks, such as prompt suggestions or the session recap. |
| `(accepted suggestion)` | A prompt sent by accepting a suggestion. |
| `app note added` | A note the desktop app added before a prompt. |
| `HANDBACK` | A subagent's report delivered back to the conversation. It is not a turn, and is recognized only when its sender is a subagent of the same session. If Claude Code flagged it as instruction-shaped, the line says `(harness flagged: ...)`. |
| `NOTICE` | A harness notice, such as a safety classifier stopping a response and switching models. |
| `NOTE` | Where recording began, when afr's hooks were added in the middle of a session. The earlier part is recovered from the transcript. |
| `*` | A highlight from today's rules on an older session, shown without changing the log. |

On Windows, use `-o` rather than piping to `Out-File`: Windows PowerShell 5.1 re-decodes
piped text with the console code page and garbles anything outside ASCII.

## Report

`afr report` writes one offline HTML page (default `afr-report-<time>.html`, or
`-o FILE.html`) for reviewing many sessions.

- **Triage list on top:** sessions still open, sessions that fail verification or had
  denials or harness notices, then every highlight by severity, each linked to its line.
  It also lists what afr did not record (see [Coverage](#coverage)).
- **Sessions grouped by date and user**, each showing the same lines as `afr replay`, with
  full prompts, inputs, and output one click down.
- **Search and filter** across all sessions, by highlight severity or by failed and denied
  calls. Failed and denied calls carry a `FAILED` or `DENIED` badge.

Choose sessions with `--since 7d`, `--since 36h`, `--since 2026-10-01`, or session ids.

The page is safe to open: it loads nothing from the network, and its Content-Security-Policy
allows only its own inline style and script. Captured text is embedded as escaped JSON and
drawn as text, never as HTML, so a prompt or tool output cannot inject markup. Secrets are
masked in the one-line summaries but not in the full values under each line. The file is
written owner-only and holds full prompts and tool output: share it only with people
allowed to see them.

## Export

`afr export` writes one zip (default `afr-export-<time>.zip`, or `-o FILE.zip`). For each
session it holds the log, its state file, a full replay (`replay-<id>.txt`), and any raw
payloads. It also holds:

- `afr-errors.log`: the whole error log, not only these sessions;
- `manifest.json`: afr's version, each session's status and integrity result, and a
  SHA-256 for every file;
- `coverage.json`: the [coverage](#coverage) result for the exported sessions and, with
  `--last`, conversations in the same period that afr has no log of. The transcripts
  themselves are not exported. `--no-coverage` leaves it out.

The files keep the data directory's layout, so after unzipping,
`AFR_HOME=<folder> afr replay <id>` works on any machine. Exporting a live session is safe,
but afr warns that its record is incomplete until the session ends. The zip holds full
prompts and tool output: share it only with people allowed to see them.

## Coverage

afr can only record what its hooks see. If the hooks are removed, even for a while,
conversations go on and afr's log still looks normal (finding F-014 in
[docs/findings.md](docs/findings.md)). `afr coverage` compares Claude Code's own
transcripts with afr's logs, prompt by prompt, and reports each conversation as:

| Status | Meaning |
| --- | --- |
| `complete` | afr recorded every prompt as it happened. |
| `late` | Everything is in afr's log, but some of it was recovered from the transcript afterwards. |
| `partial` | Some prompts are missing from afr's log; their times are listed. |
| `missed` | afr has no log of the conversation at all. |

It also counts turns that ended while other hooks ran but afr's did not. Slash commands
such as `/model` and messages the app adds itself are not prompts and are not counted;
messages the harness delivers (subagent reports, task notices) are counted separately from
typed ones.

The transcripts folder is found from afr's own sessions, else `~/.claude/projects` for
sessions recorded on this machine. `--transcripts DIR` sets it, and `--json` prints the
results. Sessions unzipped from another machine are never compared with this machine's
transcripts; their export's `coverage.json` holds the result from where they were
recorded.

`afr report` includes the same check (over the last 30 days unless `--since` is given)
unless given `--no-coverage`. It still writes a page when afr recorded nothing but the
transcripts show conversations it missed.

Coverage is advisory: transcripts are not tamper-evident, the user who can remove the hooks
can also edit or delete them, and Claude Code deletes them after about 30 days.

## Highlights

Sixteen rules tag events that deserve a reviewer's attention, such as a credential file
read, a download piped to a shell, a write outside the workspace, or the agent editing its
own hooks. Each highlight has a severity (high, medium, or low) meaning review priority;
nothing is ever blocked.

Highlights are evaluated as events are recorded and stored in the hash-chained log. When
the rules change, `afr replay` also shows what the current rules would add to older
sessions, marked with `*`, without changing the log. `--full` shows what each highlight
matched.

Organizations can add their own rules and tune or switch off built-in ones with YAML files.
The rules, their severities, the public frameworks behind them, and how to write custom
rules: [docs/highlight-rules.md](docs/highlight-rules.md).

## Try it in a sandbox

The sandbox lets you try afr on Linux without changing your own Claude Code settings:

```sh
scripts/sandbox-setup.sh            # creates ../afr-sandbox with project-level hooks
cd ../afr-sandbox && claude         # use Claude Code normally, then exit
export AFR_HOME=$PWD/.afr-data
../afr/bin/afr sessions
../afr/bin/afr replay <session-id-prefix>
```

The sandbox uses `.claude/settings.local.json`, so your global settings are untouched. It
also sets `AFR_CAPTURE_RAW=1`, which keeps every raw hook payload under `.afr-data/raw/`
for study (payloads afr cannot interpret are always kept). Raw captures contain full
prompts and command output: keep them out of git.

## More documentation

- [docs/threat-model.md](docs/threat-model.md): what afr protects, who it guards against,
  and what it does not do.
- [docs/highlight-rules.md](docs/highlight-rules.md): the highlight rules and custom rules.
- [docs/findings.md](docs/findings.md): unexpected agent and harness behavior found in
  testing, with evidence and what it means for afr.
- [docs/github-security.md](docs/github-security.md): how the repository and its Actions
  are secured.

---

## For developers

### How it works

Claude Code calls `afr hook` at each step of a session and passes it the details as JSON.
afr turns each call into normalized events and appends them to a per-session log with a
hash chain. At each prompt, turn end, and session end it also reads new lines of Claude
Code's transcript to recover what hooks do not carry: AI replies between tool calls, tool
calls the harness rejected before any hook ran, and messages the harness injected.

### Design rule: never interfere

`afr hook` always exits 0 and never writes to stdout. On any error, including a panic, it
writes to `afr-errors.log` in its data directory and exits 0. This matters because Claude
Code treats exit code 2 from a PreToolUse hook as a block, and an unrecovered Go panic
exits with status 2.

### Build and test

```sh
make build          # bin/afr
make build-windows  # bin/afr.exe
make test
make vet            # vets Linux and Windows builds
```

### Continuous integration

`.github/workflows/ci.yml` runs on every push, on Ubuntu and Windows: gofmt, `go vet` for
both targets, the full test suite (with the race detector on Linux), a cross-build of
`afr` and `afr.exe`, and the latency gate. The gate builds the real binary, times 300
`afr hook` calls on the captured payloads (with transcript catch-up on a real
transcript), and fails if p95 exceeds 50 ms. Run it locally with:

```sh
AFR_LATENCY=1 go test -run TestHookLatency -v ./cmd/afr
```

### Seeded scenario

`scripts/seeded-scenario.sh` runs a fixed nine-turn Claude Code session (`claude -p`,
Haiku by default) in a throwaway workspace and checks the recorded log against
`scripts/seeded-scenario.expected.json`. The turns cover normal work, a subagent, resume
and continue, and each threat in the threat model, using harmless decoys (fake keys, a
fake `.bashrc`, a scratch git repo, a web server bound to 127.0.0.1). It uses your
Claude quota, so it is a manual end-to-end check, not part of CI.

```sh
scripts/seeded-scenario.sh            # prints PASS / FAIL / SKIPPED per check
```

- **PASS:** afr recorded it.
- **FAIL:** afr missed something the transcript shows the model did.
- **SKIPPED:** the model never attempted that action (it is absent from Claude Code's
  transcript too), which is model behavior, not an afr miss.
- **PENDING:** a check set as not yet required.

Each highlight check names the rule and severity it expects. Deterministic coverage lives
in the unit tests.

What the script grants `claude -p`: Read, Write, Edit, and Agent without restriction, and
shell permission only for the exact commands its turns dictate. Commands run against your
real machine, confined only by the working directory, so read the script before running it.

### Data layout

Data directory: `$AFR_HOME`, else `$XDG_DATA_HOME/afr` or `~/.local/share/afr` on Linux,
and `%LOCALAPPDATA%\afr` on Windows.

Permissions differ by platform:

- **Linux:** files are created owner-only (mode 600) and directories mode 700.
- **Windows:** afr does not set ACLs yet, so files inherit the permissions of the folder
  they are in. Under the default `%LOCALAPPDATA%\afr` that normally means only you,
  SYSTEM, and Administrators. If `AFR_HOME` points outside your user profile (for example
  `C:\afr-data`), other local users may be able to read the logs, so keep it under your
  profile. Owner-only ACLs are planned.

Inside the data directory:

- `sessions/<session-id>.ndjson`: one event per line (schema in `internal/schema`), each
  with `seq`, `prev_hash`, and `hash`. The hash covers `content_hash` instead of the
  content, so content can be purged later without breaking the chain. Events recovered
  from the transcript carry `"origin": "transcript"`. Events are in write order (`seq`),
  so sort by `ts` to read a session in the order things happened.
- `sessions/<session-id>.state.json`: the transcript read offset and the ids already
  logged, so recovery never records anything twice. If it is lost, it is rebuilt from the
  log. `sessions/<session-id>.ndjson.lock` is the per-session write lock.
- `raw/<session-id>/`: raw payloads (opt-in, or when a payload is not recognized).
- `afr-errors.log`: anything that went wrong inside a hook call.

Test fixtures in `testdata/claude-code/<version>/` are real payloads with user names
scrubbed.
