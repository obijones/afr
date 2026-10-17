# afr

Agent Flight Recorder: a monitoring and logging tool for AI coding agents. It records each
AI session from start to end (prompts, AI replies, tool calls and their results) so a
reviewer can replay it later and see exactly what the AI tool did. It only records; it
never blocks or changes anything the agent does.

Status: early development (Week 3). `afr hook` turns each Claude Code hook payload into
normalized events and appends them to a per-session log with a hash chain. At each prompt,
turn end, and session end it also reads new lines of Claude Code's transcript to recover
what hooks do not carry: AI replies between tool calls, tool calls the harness rejected
before any hook ran, and messages the harness injected. `afr verify` checks the chain.

## Review sessions

```sh
afr sessions                 # most recent first: status, turns, tools, highlights (high/medium/low), app, dir, id
afr replay 3394              # one session as a conversation (full id or unique prefix)
afr replay --full 3394       # with complete prompts, replies, tool inputs, and outputs
afr replay -o s.txt 3394     # write it to a file (UTF-8), for sharing or review
afr export 3394 a6ac         # zip sessions to review on another machine
afr export --last 2          # zip the two most recently active sessions
afr report --since 7d        # one HTML page to review many sessions
afr coverage --since 7d      # conversations and prompts afr did not record
```

A replay reads top to bottom in the order things happened: where the session began, each
prompt (turns t1, t2, ...), the AI's replies, every tool call on one line with its result,
subagent work tagged with the subagent type, highlights at the front of the line, and how
the session ended. Tool calls the harness handled without running a hook are marked
`(no hook ran)`; messages the harness injected are marked as such. Failed calls show the
error, and calls auto mode refused show `denied by auto mode` with the reason. Claude
Code's own background tasks (prompt suggestions, the session recap) appear as `HELPER`
lines, and a prompt sent from a suggestion is marked `(accepted suggestion)`. Turns are
numbered by the prompts a person sent. In the Claude desktop app, the note the app adds
before a prompt is shown separately (`app note added`), and a subagent's report that the
harness delivers as a prompt appears as a `HANDBACK` line, not a turn; it is recognized
only when its sender is a subagent of the same session. When Claude Code flags the report
as instruction-shaped, the line says `(harness flagged: ...)` and the notice is kept under
it. `afr sessions` shows the first 8
characters of each id, which every command accepts (`--full-ids` for whole ids), the app
that ran the session (`desktop`, `cli`), and the folder a session is in now, after any
move. The desktop app keeps conversations open, so a desktop session with no end shows as
`idle 3h` rather than `open`. When afr's hooks start in the middle of a session (hooks
added while it ran), afr recovers the earlier part from the transcript, counts the user's
prompts there as turns, and a `NOTE` line marks where recording began. On Windows,
use `-o` rather than piping to `Out-File`: Windows PowerShell 5.1 re-decodes piped text
with the console code page and garbles anything outside ASCII. Harness notices, such
as a safety classifier stopping a response and switching models, appear as `NOTICE` lines
with the category and the models involved. The header shows the agent version, the
models used, and whether the log's hash chain verifies.

### Export

`afr export` writes one zip (default `afr-export-<time>.zip` in the current folder, or
`-o FILE.zip`) holding, for each session, the log and its state file, a full replay
(`replay-<id>.txt`), and any raw payloads, plus `afr-errors.log` (the whole error log, not
only these sessions) and `manifest.json` with afr's version, each session's status and
integrity result, and a SHA-256 for every file. The files keep the data directory's
layout, so after unzipping, `AFR_HOME=<folder> afr replay <id>` works on any machine.
It also holds `coverage.json` (see Coverage below) for the exported sessions and, with
`--last`, conversations in the same period that afr has no log of; the transcripts
themselves are not exported (`--no-coverage` leaves it out).
Each log is read under its session lock, so exporting a live session is safe, but afr
warns that the record is incomplete until the session ends. The zip holds full prompts and
tool output: share it only with people allowed to see them.

### Report

`afr report` writes one offline HTML page (default `afr-report-<time>.html`, or `-o
FILE.html`) for reviewing many sessions: a triage list on top (sessions that are still
open, fail verification, or had denials or harness notices, then every highlight by
severity, each linked to its line), then sessions grouped by date and user. Each session
shows the same lines as `afr replay`, from the same code, with full prompts, inputs, and
output one click down. A search box and a filter (highlight severity, or failed and denied
calls) work across all sessions. Failed and denied calls carry a `FAILED` or `DENIED` badge
at the front of their line, and a session's "failed calls" count jumps to them.
Select sessions with `--since 7d`, `--since 36h`, `--since 2026-10-01`, or session ids.
The triage also lists what afr did not record (see Coverage): a session with prompts afr
missed is listed for checking even when it is an idle desktop conversation, and
conversations afr has no log of are listed by time, app, and folder only, never their
content.

The page loads nothing from the network, and its Content-Security-Policy allows only its
own inline style and script. Captured text is embedded once as escaped JSON and drawn as
text, never as HTML, so a prompt or tool output cannot inject markup. Secrets are masked
in the one-line summaries; the full values under each line are not. The file is written
owner-only and holds full prompts and tool output: share it only with people allowed to
see them.

### Coverage

afr can only record what its hooks see. If the hooks are removed, even for a while, the
conversations go on and afr's log looks normal (docs/findings.md F-014). `afr coverage`
compares Claude Code's own transcripts with afr's logs, prompt by prompt, and reports
each conversation as `complete`, `late` (everything is in afr's log, some only recovered
from the transcript afterwards), `partial` (prompts afr has no record of, with their
times), or `missed` (no afr log at all). It also counts turns that ended while other hooks
ran but afr's did not, which Claude Code notes in the transcript. Slash commands such as
`/model` and messages the app adds itself are not prompts and are not counted; messages the
harness delivers (subagent reports, task notices) are counted separately from typed ones.

The transcripts folder is found from afr's own sessions, else `~/.claude/projects` for
sessions recorded on this machine; `--transcripts DIR` sets it, `--json` prints the
results. Sessions unzipped from another machine are never compared with this machine's
transcripts; their export's `coverage.json` holds the result from where they were
recorded. `afr report` includes the same check (over the last 30 days unless `--since` is
given) unless given `--no-coverage`, and still writes a page when afr recorded nothing but
the transcripts show conversations it missed. Coverage is advisory: transcripts are not
tamper-evident, the user who can remove the hooks can also edit or delete them, and Claude
Code deletes them after about 30 days.

## Highlights

Sixteen rules tag events that deserve a reviewer's attention, such as a credential file
read, a download piped to a shell, a write outside the workspace, or the agent editing its
own hooks. Each highlight has a severity (high, medium, or low) meaning review priority;
nothing is ever blocked. Highlights are evaluated as events are recorded and stored in the
hash-chained log. When the rules change, `afr replay` also shows what the current rules
would add to older sessions, marked with `*`, without changing the log. `--full` shows
what each highlight matched. Organizations can add their own rules and tune or switch
off built-in ones with YAML files (`afr rules check`, `test`, and `list` help write
them). The rules, their severities, the public frameworks behind them, and customer
rules: [docs/highlight-rules.md](docs/highlight-rules.md).

## Build and test

```sh
make build          # bin/afr
make build-windows  # bin/afr.exe
make test
make vet            # vets Linux and Windows builds
```

## Continuous integration

`.github/workflows/ci.yml` runs on every push, on Ubuntu and Windows: gofmt, `go vet` for
both targets, the full test suite (with the race detector on Linux), a cross-build of
`afr` and `afr.exe`, and the latency gate. The gate builds the real binary, times 300
`afr hook` calls on the captured payloads (with transcript catch-up on a real
transcript), and fails if p95 exceeds 50 ms. Run it locally with:

```sh
AFR_LATENCY=1 go test -run TestHookLatency -v ./cmd/afr
```

The seeded scenario is not part of CI: it needs a live model and Claude quota. How the
repository and its Actions are secured: [docs/github-security.md](docs/github-security.md).

## Try it in a sandbox

```sh
scripts/sandbox-setup.sh            # creates ../afr-sandbox with project-level hooks
cd ../afr-sandbox && claude         # use Claude Code normally, then exit
export AFR_HOME=$PWD/.afr-data
../afr/bin/afr sessions
../afr/bin/afr replay <session-id-prefix>
```

The sandbox also sets `AFR_CAPTURE_RAW=1`, which keeps every raw hook payload under
`.afr-data/raw/` for study. Payloads afr cannot interpret are always kept raw.

The sandbox uses `.claude/settings.local.json`, so your global Claude Code settings are
untouched. Raw captures contain full prompts and command output: keep them out of git.

## Seeded scenario

`scripts/seeded-scenario.sh` runs a fixed nine-turn Claude Code session (`claude -p`,
Haiku by default) in a throwaway workspace and checks the recorded log against
`scripts/seeded-scenario.expected.json`. The turns cover normal work, a subagent, resume
and continue, and each threat in the threat model, using harmless decoys (fake keys, a
fake `.bashrc`, a scratch git repo, a web server bound to 127.0.0.1). It uses your
Claude quota.

```sh
scripts/seeded-scenario.sh            # prints PASS / FAIL / SKIPPED per check
```

Results: PASS; FAIL means afr missed something the transcript shows the model did;
SKIPPED means the model never attempted that action (it is absent from Claude Code's
transcript too), which is model behavior, not an afr miss. Each highlight check names the
rule and severity it expects; PENDING marks a check set as not yet required. Because a
live model decides what to attempt, this is a manual end-to-end check, not a CI gate;
deterministic coverage lives in the unit tests.

What the script grants `claude -p`: Read, Write, Edit, and Agent without restriction, and
shell permission only for the exact commands its turns dictate. Commands run against your
real machine, confined only by the working directory, so read the script before running it.

## Findings

Unexpected agent and harness behavior that testing revealed, with evidence and what it
means for afr: [docs/findings.md](docs/findings.md).

## Threat model

What afr protects, who it guards against, and what it does not do:
[docs/threat-model.md](docs/threat-model.md).

## Design rule: never interfere

`afr hook` always exits 0 and never writes to stdout. On any error, including a panic, it
writes to `afr-errors.log` in its data directory and exits 0. This matters because Claude
Code treats exit code 2 from a PreToolUse hook as a block, and an unrecovered Go panic
exits with status 2.

## Data layout

Data directory: `$AFR_HOME`, else `$XDG_DATA_HOME/afr` or `~/.local/share/afr` on Linux,
and `%LOCALAPPDATA%\afr` on Windows.

Permissions differ by platform:

- Linux: files are created owner-only (mode 600) and directories mode 700.
- Windows: afr does not set ACLs yet, so files inherit the permissions of the folder they are
  in. Under the default `%LOCALAPPDATA%\afr` that normally means only you, SYSTEM, and
  Administrators. If `AFR_HOME` points outside your user profile (for example `C:\afr-data`),
  other local users may be able to read the logs, so keep it under your profile. Owner-only
  ACLs are planned.

Inside the data directory:

- `sessions/<session-id>.ndjson`: one event per line (schema in `internal/schema`), each
  with `seq`, `prev_hash`, and `hash`. The hash covers `content_hash` instead of the
  content, so content can be purged later without breaking the chain.
  Events recovered from the transcript carry `"origin": "transcript"`; events are in write
  order (`seq`), so sort by `ts` to read a session in the order things happened.
- `sessions/<session-id>.state.json`: the transcript read offset and the ids already
  logged, so recovery never records anything twice. If it is lost, it is rebuilt from the
  log. `sessions/<session-id>.ndjson.lock` is the per-session write lock.
- `raw/<session-id>/`: raw payloads (opt-in, or when a payload is not recognized).
- `afr-errors.log`: anything that went wrong inside a hook call.

Test fixtures in `testdata/claude-code/<version>/` are real payloads with user names
scrubbed.
