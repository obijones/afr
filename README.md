# afr

Agent Flight Recorder: a monitoring and logging tool for AI coding agents. It records each
AI session from start to end (prompts, AI replies, tool calls and their results) so a
reviewer can replay it later and see exactly what the AI tool did. It only records; it
never blocks or changes anything the agent does.

Status: early development (Week 1). The `afr hook` command currently saves each raw hook
payload for study; the normalized event log and replay come next.

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
ls .afr-data/raw/*/                 # one JSON file per hook call
```

The sandbox uses `.claude/settings.local.json`, so your global Claude Code settings are
untouched. Raw captures contain full prompts and command output: keep them out of git.

## Design rule: never interfere

`afr hook` always exits 0 and never writes to stdout. On any error, including a panic, it
writes to `afr-errors.log` in its data directory and exits 0. This matters because Claude
Code treats exit code 2 from a PreToolUse hook as a block, and an unrecovered Go panic
exits with status 2.

Data directory: `$AFR_HOME`, else `~/.local/share/afr` on Linux, `%LOCALAPPDATA%\afr` on
Windows.
