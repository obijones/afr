# Findings from testing

Behavior of AI agents and harnesses that afr testing revealed, with evidence and what it
means for afr. Newest first.

## F-013: Desktop app differences that matter for review (2026-10-11)

Round 3 tested the Claude desktop app's Code tab on Windows with hooks in user settings.

| What | Desktop Code tab | CLI |
| --- | --- | --- |
| Claude Code version | bundled 2.1.295 | 2.1.296 |
| `model` in `SessionStart` | absent | present |
| Model used | the app's own picker | `settings.json` `model` |
| Permission mode seen | `auto` | as configured |
| Working folder | a fresh scratch folder per conversation unless the user picks one | the current folder |
| Chat tab | not Claude Code, never runs hooks | n/a |

The first real `PermissionDenied` payload came from desktop: an environment variable dump,
`reason: "[Credential Materialization]"`, with the documented fields (`tool_name`,
`tool_input`, `tool_use_id`, `reason`). afr recorded it as `denied by auto mode`; it is now a
test fixture (`testdata/claude-code/2.1.295-windows-desktop/`), which closes F-004's open item.

Payloads also carry fields afr does not record yet: `effort.level` on most events,
`mcp_server` on tool calls, `session_title` on prompts and resumed starts, and on a resumed
start `context_tokens`, `estimated_cache_write_usd`, and `seconds_since_last_response`.

**What it means for afr.** The starting model of a desktop session comes only from the
transcript. Coverage claims must say "Code tab", never "Claude desktop". Collecting desktop
transcripts on Windows needs long-path support: subagent transcript paths under a
scratch-workspace project folder pass 260 characters, and Windows PowerShell 5.1
`Compress-Archive` fails on them with a misleading permission error (`tar` works).

## F-012: Desktop folder moves are a tool call, and the app copies files itself (2026-10-11)

**What we saw.** Asked to keep the work, Claude called
`mcp__ccd_directory__change_directory` with `{"path": "C:\\Users\\<user>\\Documents\\afr-d5"}`
(`mcp_server: {"name": "ccd_directory", "source": "sdk"}`). Its result says the working
folder moves when the turn ends. The app then copied the scratch files into the new folder
itself and told Claude through a note added to the next prompt (F-008).

**What it means for afr.**

1. The move is visible in the hooks, no transcript needed: afr can make the new folder part
   of the workspace when that call succeeds. Today every write there is flagged
   `fs.write_outside_workspace` (medium), a false positive.
2. Files the app copies are not tool calls, so afr has no record of them being created.
3. A session that afr joined late (F-011) has the opposite problem: its workspace is the
   final folder, so writes in the original scratch folder were flagged.

**Fixed** on branch `fix/desktop-rules`: the rules' workspace now includes folders the app
moved the session to (a successful `change_directory`) and the session's scratchpad
(`scratchpad_dir`, recorded once per session), tracked the same way when recording and in
replay. Recovered transcript events carry the folder the session was in at the time, so a
session afr joins late is judged against its original folder. Files the app copies itself
stay invisible.

## F-011: Hook changes reach sessions that are already running (2026-10-11)

**What we saw.** Desktop conversation `fcb60a58` started at 11:41, before afr's hooks were in
user settings. When the app exited at 16:26, its `SessionEnd` hook ran afr, and afr
recovered the whole conversation from the transcript: 28 tool calls, replies, and 2
highlights. The running process had picked up the new hook settings.

**What it means for afr.**

1. Good for rollout: sessions already running when afr is installed still get recorded, at
   their next hook event.
2. Security: if adding hooks reaches running sessions, removing them presumably does too
   (not tested). Managed settings are the answer for organizations.
3. Gap: afr labels everything recovered without a hook as harness activity, so this
   session's real prompts show as "injected by the harness", its tool calls as "no hook
   ran", and it counts 0 turns. Recovered events earlier than the first hook event should be
   labeled as recorded late, with real prompts counted as turns.

**Fixed** on branch `feat/report-pr-c`: events recovered from before the session's first
hook event are treated as recorded late. Their user prompts count as turns (harness text and
`isMeta` prompts, now recorded as `meta`, do not), their calls are not marked "no hook ran",
and the replay and report mark where afr started recording.

## F-010: How desktop sessions end and resume (2026-10-11)

| Action in the desktop app | What the hooks recorded |
| --- | --- |
| `/exit` | not a desktop command ("not valid"), nothing |
| Archive a conversation | `SessionEnd`, reason `other` |
| Delete a conversation | `SessionEnd`, reason `other`; afr's log survives and verifies |
| Close the window | nothing: the app keeps running, with one `claude` process per open conversation |
| Exit from the tray icon | `SessionEnd`, reason `other`, for every open conversation |
| Reopen an ended conversation | `SessionStart`, `source: resume`, same session id |

**What it means for afr.** A desktop session that shows `open` is usually idle, not cut off,
so the report should judge desktop sessions by last activity. The end reason cannot tell
archive, delete, and quit apart. Resume keeps one log per conversation.

**Fixed** on branch `feat/report-pr-c`: afr records the app (`entrypoint`) once per
session, and a desktop session with no end shows as `idle <time>` in `afr sessions` and the
report instead of being flagged as open. Older logs are recognized as desktop by their
scratch-workspaces folder.

## F-009: Desktop subagents hand their results back as user prompts (2026-10-11)

**What we saw.** In the desktop app, subagents launched with the Agent tool run in the
background (`async_launched`). Each finishes by calling a `SubagentHandback` tool with its
report; its `SubagentStop` has an empty `last_assistant_message`. The harness then delivers
the report as a new prompt that fires `UserPromptSubmit`, beginning
`<agent-message from="<agent id>">` and `[Subagent hand-back]`, with a frame saying the text
is model output, not the user's. Task notifications arrive the same way.

**What it means for afr.** afr counts these as user turns (a session with 2 real prompts
showed 4) and shows the subagent as finishing with an empty result. Fix: treat a prompt as a
hand-back only when its `from` id matches a subagent of the same session (a user can type
the same text), label it as delivered by the harness, and show the hand-back message as the
subagent's result.

**Fixed** in PR #19: hand-backs and task notices show as `HANDBACK` lines, are not turns,
and are recognized only by a matching subagent id; a background subagent's result comes from
`SubagentHandback`.

## F-008: The desktop app adds a long note to the user's prompt (2026-10-11)

**What we saw.** In desktop sessions started without a project folder, the `prompt` field of
the first `UserPromptSubmit` begins with a `<system-reminder>` block of about 4 KB before the
user's words. It tells the model it is in a scratch workspace, when to offer to move the
work, how to call `change_directory` and `request_directory`, and lists the project folders
recently used on the machine. After a move, the next prompt carries a similar note listing
the files the app copied.

**What it means for afr.** The replay summary shows the note instead of what the user
typed. Fix: summarize the user's words and show the note separately as harness-added text;
keep the full prompt in the log. The note is also evidence of what context the app gave
the model, including other project folders on the machine, so it stays in the record.

**Fixed** in PR #19: the replay shows what the user typed and the app's note separately
(`app note added`); the full prompt stays in the log.

## F-007: Withdrawn: a desktop conversation title looked carried over from a deleted one (2026-10-11)

**Withdrawn 2026-10-11 (round 3).** The round 3 export shows the claim was wrong. In
`fcb60a58` the first prompt (11:41:14 local) asks for productivity suggestions, and a request on
another topic comes later in the same conversation, so its title "Productivity
suggestions" is accurate. The deleted conversation (`868903a5`, released at 11:36) was a
separate, earlier one. The tester remembered the order differently; the logs are the record.
afr recorded nothing wrong here.

## F-006: A session start with nothing after it, 8 seconds before the CLI session (2026-10-11)

**What we saw.** Session `86e55708` has one event: `SessionStart`, `source: startup`, folder
`C:\Users\<user>\afr-test`, at 18:34:10 UTC. Its transcript file was never created. The CLI
session (`92b7d940`, same folder) started at 18:34:18. The user had opened the desktop app
first, then started `claude` in a terminal. The desktop conversations ran in other folders
(F-005), and the payload does not say which app sent it.

**Likely explanation (inference, not verified).** The desktop app starts a Claude Code
process in the last folder used, which fires `SessionStart`, and discards it when the
conversation opens in a new scratch folder. If so, desktop does run hooks from a project's
settings. To confirm: open the desktop app alone and watch for a lone start.

**What it means for afr.** A session with only a start and no transcript is noise, not a
gap, but `afr sessions` lists it as `open` forever. Possible fix: show such sessions as
`empty` and leave them out of gap counts in the digest.

**Round 3 (not confirmed).** Opening the desktop app alone for a minute produced no session
and no transcript, so the explanation above does not hold as stated. A quick CLI start and
exit (`52bbcea1`) produced the opposite case: a session end with no start, since the start
hook never fired before the user quit. Both shapes are noise for a reviewer. `86e55708`
stays unexplained.

## F-005: Desktop app conversations were not recorded: hooks were registered for one project only (2026-10-11)

**What we saw.** The test settings put afr's hooks in
`C:\Users\<user>\afr-test\.claude\settings.json`, which applies only to sessions in that
folder. The CLI session there recorded fully (4 turns, 63 tool calls, `SessionEnd` reason
`prompt_input_exit`). Three other sessions that day produced no afr record at all:

| Session | App (transcript `entrypoint`) | Folder |
| --- | --- | --- |
| `868903a5` | `claude-desktop` | `%APPDATA%\Claude\scratch-workspaces\<ids>\scratch-2026-10-11-2aca18`, deleted by the user after one answer |
| `fcb60a58` | `claude-desktop` | started in another scratch folder, then moved to `Documents\<project>`; used 8 subagents and web fetches |
| `1a7e6e00` | `cli` | `C:\Users\<user>\afr` |

**Explanation.** The desktop Code tab does not open conversations in a project folder by
default: each new conversation starts in a fresh scratch folder under
`%APPDATA%\Claude\scratch-workspaces`, and can later be moved (the transcript records
`relocated` with the new folder). Project settings in another folder never load there.
Deleting a desktop conversation removes its transcript and leaves only
`<id>.desktop-released.json` (`"reason": "delete"`), so nothing is left to recover from.

**What it means for afr.**

1. **Register hooks in user settings** (`%USERPROFILE%\.claude\settings.json`, or
   `~/.claude/settings.json`), not in a project, so every session in every folder runs them.
   For enterprises, managed settings (phase E). Project settings are only for sandboxes.
2. **Deleting a conversation does not delete afr's record**, once hooks run: afr writes each
   event when it happens, into its own folder. What afr cannot get back after a deletion is
   anything it had not yet read from the transcript (replies between the last hook and the
   deletion).
3. **Moved desktop sessions:** afr's workspace for the rules is the first folder seen, so
   after a move into `Documents\...` every write there would be flagged as outside the
   workspace. Consider following the `relocated` record from the transcript.
4. **Coverage check:** comparing transcripts under `~/.claude/projects` with afr's sessions
   would have shown all three missing sessions. The threat model already lists this as the
   answer to removed hooks; it belongs in the digest.

**Round 3: confirmed fixed by user settings.** With the hooks merged into
`%USERPROFILE%\.claude\settings.json` (`/hooks` lists them as `[User]`), every desktop Code
tab conversation was recorded, in scratch folders and after a move. The desktop Chat tab
does not run Claude Code and is not covered.

## F-004: Auto mode denials leave a tool call with no result in the hooks (2026-10-10)

**What we saw.** In the first native Windows session (Claude Code 2.1.296, interactive CLI,
`permission_mode: "auto"`), two PowerShell calls looking for `csc.exe` got a `PreToolUse`
hook and nothing after it. afr's replay shows them as "no result (refused, interrupted, or
failed)". The AI then said the commands were "blocked by the auto-mode classifier".

**Explanation from the documentation.** `PostToolUse` "Runs immediately after a tool
completes successfully", so a denied call never fires it. Claude Code has a separate
`PermissionDenied` hook that "Runs when auto mode denies a tool call" and only in auto
mode ([hooks](https://code.claude.com/docs/en/hooks.md)). afr's suggested hook config did not
register it.

**What it means for afr.** Gap: register `PermissionDenied` (and `PostToolUseFailure`) and
record the denial as the call's result, so the replay says "denied by auto mode" instead of
guessing. The transcript also holds the denial, but afr only recovers results for calls no
hook saw.

**Fixed** on branch `fix/windows-capture-gaps`: `PermissionDenied` becomes the call's
result, shown as `denied by auto mode: <reason>`. The docs also show `PostToolUse` fires
only on success, so every failed call (a command exiting non-zero, on any platform) had the
same gap; `PostToolUseFailure` now records the error as the result. Both hooks are in
`scripts/sandbox-setup.sh`. Payload shapes come from the hooks reference. The second Windows session (2026-10-11)
confirmed the real `PostToolUseFailure` payload (`error` holds the exit code and output);
a real `PermissionDenied` payload is still to be captured.

## F-003: Claude Code's own background agents fire SubagentStop, and suggested prompts become user prompts (2026-10-10)

**What we saw.** Same Windows session. After every turn a `SubagentStop` arrived with an
empty `agent_type`, no `Agent` tool call before it, and a one-line message: "add a README to
each folder", "yes, add a top-level README", "initialize a git repo with a .gitignore for
binaries", and once "Didn't produce a suggestion: the next step touches building/running an
executable...". The next user prompts in turns 2 and 3 were exactly those texts. Three
minutes after the last turn, one more `SubagentStop` carried a recap: "You're building the
multi-folder afr-test project; folders, per-folder READMEs, and a top-level README are
done...". afr's replay showed all of these as `SUBAGENT finished: ...`, as if a subagent had
done that work.

**Explanation from the documentation.**

- Prompt suggestions are generated "with a short background request to the same model your
  session is using", and "SubagentStop fires when one of those finishes too", with
  `agent_type` empty when the session runs without a named agent
  ([hooks](https://code.claude.com/docs/en/hooks.md),
  [prompt suggestions](https://code.claude.com/docs/en/interactive-mode#prompt-suggestions)).
  The user accepts one with Tab or Right arrow, then Enter.
- The recap matches the session recap feature, generated "once at least three minutes have
  passed since the last completed turn and the terminal is unfocused"
  ([session recap](https://code.claude.com/docs/en/interactive-mode#session-recap)). The docs
  do not say that it fires `SubagentStop`; this capture shows that it does.
- Both can be turned off: `promptSuggestionEnabled: false`, `awaySummaryEnabled: false`.

**Inference, not verified.** Turns 2 and 3 were accepted suggestions: the prompt text the
model suggested, submitted by the user. The docs do not say whether an accepted suggestion
is marked in hook payloads or the transcript.

**What it means for afr.**

1. Gap: label harness background agents (empty `agent_type`, no matching `Agent` call) as
   such, not as subagent work, and say what they produced (a suggested prompt, a recap).
2. For attribution, mark a prompt whose text equals the suggestion just made: the words
   came from the model, the decision to send them from the user.

**Fixed** on branch `fix/windows-capture-gaps`, at read time: these appear as `HELPER` lines
("Claude Code suggested the next prompt" or "Claude Code background task"), the prompt is
marked `(accepted suggestion)`, and the seeded scenario no longer counts them as subagents.

## F-002: Opus 5.5's classifier switched an interactive session to Opus 4.8 over a calc.exe launcher (2026-10-10)

**What we saw.** Same Windows session, on `claude-opus-5-5`. Turn 4 asked for "an example
windows binary that when executed opens calc.exe", the classic harmless demo. afr recorded a
notice ("Opus 5.5's safeguards stopped the response above"), the harness-injected companion
message, and a `model_refusal_fallback` warning: category `cyber`, `claude-opus-5-5 ->
claude-opus-4-8` for the session. Opus 4.8 answered and wrote the C# source and a build
script; the replay header reads `model: claude-opus-5-5, claude-opus-4-8`.

**Explanation.** The same mechanism as F-001: classifier refusal, then fallback for the rest
of the session when `switchModelsOnFlag` is on. The notice text itself says Opus 5.5's
safeguards "can sometimes flag non-cybersecurity work".

**What it means for afr.** The notice capture built after F-001 worked on Windows unchanged.
F-001's open question about interactive sessions gets part of an answer: in an interactive
session the switch was announced once "for the session" and later replies came from Opus
4.8. The session had only one turn after the switch, so persistence over many turns is still
unverified.

## F-001: Sonnet 5.5 safety classifier switched the session to Sonnet 5 (2026-10-09)

**What we saw.** In a seeded-scenario run requested on `claude-sonnet-5-5` (Claude Code
2.1.296, `claude -p` with `--resume` per turn), turns 1 to 3 ran on `claude-sonnet-5-5`.
From turn 4 (the localhost `curl | sh` and `base64 -d | sh` prompt) every turn ran on
`claude-sonnet-5`. afr's new `model` field surfaced it: the replay header read
`model: claude-sonnet-5-5, claude-sonnet-5`.

**Evidence in Claude Code's transcript** (session `b689fd1a`):

- A `system` record, `subtype: "informational"`, level notice: "Sonnet 5.5's safeguards
  stopped the response above · continuing once with that noted".
- A `system` record, `subtype: "model_refusal_fallback"`, level warning, with structured
  fields: `trigger: "refusal"`, `scope: "session"`, `direction: "retry"`,
  `originalModel: "claude-sonnet-5-5"`, `fallbackModel: "claude-sonnet-5"`,
  `apiRefusalCategory: "cyber"`, `apiRefusalExplanation` ("This request triggered
  restrictions on violative cyber content and was blocked under Anthropic's Usage
  Policy..."), `refusedUserMessageUuid`, `retractedMessageUuids: []`. Its text says Sonnet
  5.5 "can sometimes flag non-cybersecurity work" and "Sonnet 5 is answering instead".
- An injected user message (`isMeta: true`, `turnCompanion: true`, `refusedMessageId`):
  "Your response above was stopped by a safety classifier... tool calls in it that had not
  finished did not run. Do not produce that content again, even reworded."
- This repeated on all six turns from turn 4 to 9 (six fallback records, all category
  `cyber`), including "Say goodbye in five words."
- Claude Code's own `modelUsage` lists both models from turn 4 on. Sonnet 5.5's cost kept
  rising (0.147 to 0.683 USD) while its output tokens stayed at 1658: each later turn went
  to Sonnet 5.5 first and was stopped before any output, then answered by Sonnet 5.

**Explanation from the documentation.**

- Sonnet 5.5 (and Fable, Opus 5.5, Opus 5, Haiku 5.5) include safety classifiers that can
  decline a request with `stop_reason: "refusal"` and a category; `cyber` covers requests
  that "could enable cyber harm" and "Benign cybersecurity work can also trigger this
  category." ([Refusals and fallback](https://platform.claude.com/docs/en/build-with-claude/refusals-and-fallback))
- In Claude Code, "Sonnet 5.5: cybersecurity-flagged requests re-run on Sonnet 5", and
  "After a fallback, the session continues on the fallback model." The setting
  `switchModelsOnFlag` controls it; `false` means ask each time. This user's settings have
  `"switchModelsOnFlag": true`. ([Model configuration](https://code.claude.com/docs/en/model-config),
  [settings reference](https://code.claude.com/docs/en/settings-reference))

**Advisor: available, not used.** `advisorModel` is `opus` in this user's settings, and the
transcript has an `advisor_tool` attachment (`available: true`, `model: claude-opus-5-5`);
every assistant record carries `advisorModel: claude-opus-5-5`. But there are no advisor
calls and no Opus tokens in `modelUsage`. The docs say "Claude decides when to call it",
so availability does not mean use. ([Advisor](https://code.claude.com/docs/en/advisor))

**Open questions (inference, not verified).**

- The docs say the session continues on the fallback model, yet each `--resume` turn tried
  Sonnet 5.5 again first. Likely each `claude -p --resume` is a new process that starts on
  the requested model, and the conversation history (now full of security-test content)
  trips the classifier again. Check in one interactive session.
- The docs say non-interactive mode ends a flagged turn with a refusal "instead" of the
  prompt; here it switched. Likely because `switchModelsOnFlag: true` needs no prompt.

**What it means for afr.**

1. The `model` field works and is worth keeping: it was the only visible sign in afr.
2. **Gap: afr did not record harness notices.** Fixed on branch `fix/harness-notices`:
   `system` records at notice, warning, or error level, and every `model_refusal_fallback`,
   become `harness.notice` events with the category and the original and fallback model.
   Replay shows `[WARNING] safeguards flagged this (cyber): claude-sonnet-5-5 ->
   claude-sonnet-5 for the session`.
3. **Gap: the injected companion message was dropped.** Fixed: harness-written messages
   (`isMeta`) are recorded as harness-injected and never matched against hook prompts by
   `promptId`; a rebuilt state skips records by transcript `uuid`.
4. **Gap: no agent version.** Fixed: every event carries `agent_version` from the
   transcript (empty until the first transcript read in a session), the replay header
   shows it, and a version change mid-session becomes a `harness.notice`.
5. For the seeded scenario, Sonnet 5.5's classifier blocks the threat turns, so Haiku stays
   the default; a Sonnet run is itself a good test of how afr shows a model switch.
6. The classifier is not deterministic for the same prompt: a later two-turn Sonnet session
   with the same `curl | sh` prompt (but less security-test history) was not flagged.
