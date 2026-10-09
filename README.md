# afr

Agent Flight Recorder: a monitoring and logging tool for AI coding agents. It records each
AI session from start to end (prompts, AI replies, tool calls and their results) so a
reviewer can replay it later and see exactly what the AI tool did. It only records; it
never blocks or changes anything the agent does.

Status: early development (Week 2). `afr hook` turns each Claude Code hook payload into
normalized events and appends them to a per-session log with a hash chain. At each prompt,
turn end, and session end it also reads new lines of Claude Code's transcript to recover
what hooks do not carry: AI replies between tool calls, tool calls the harness rejected
before any hook ran, and messages the harness injected. `afr verify` checks the chain.

## Review sessions

```sh
afr sessions                 # most recent first: status, turns, tool calls, user@host, dir
afr replay 3394              # one session as a conversation (full id or unique prefix)
afr replay --full 3394       # with complete prompts, replies, tool inputs, and outputs
```

A replay reads top to bottom in the order things happened: where the session began, each
prompt (turns t1, t2, ...), the AI's replies, every tool call on one line with its result,
subagent work tagged with the subagent type, highlights at the front of the line, and how
the session ended. Tool calls the harness handled without running a hook are marked
`(no hook ran)`; messages the harness injected are marked as such. The header shows
whether the log's hash chain verifies.

## Build and test

```sh
make build          # bin/afr
make build-windows  # bin/afr.exe
make test
make vet            # vets Linux and Windows builds
```

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

## Design rule: never interfere

`afr hook` always exits 0 and never writes to stdout. On any error, including a panic, it
writes to `afr-errors.log` in its data directory and exits 0. This matters because Claude
Code treats exit code 2 from a PreToolUse hook as a block, and an unrecovered Go panic
exits with status 2.

## Data layout

Data directory: `$AFR_HOME`, else `~/.local/share/afr` on Linux, `%LOCALAPPDATA%\afr` on
Windows. All files are owner-only.

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
