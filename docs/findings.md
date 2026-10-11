# Findings from testing

Behavior of AI agents and harnesses that afr testing revealed, with evidence and what it
means for afr. Newest first.

## F-007: A new desktop conversation kept the deleted conversation's title (2026-10-11)

**What we saw.** On Windows, the user asked the Claude desktop app (Code tab) for
productivity suggestions, then deleted that conversation and started a new one about lucid
dreaming. The new conversation's transcript (`fcb60a58`) records `custom-title` and
`agent-name` as "Productivity suggestions" from its first lines to its last, 17 times,
although no prompt in it is about productivity.

**What it means for afr.** Nothing to fix; afr does not record titles. A reviewer should not
trust a desktop conversation's title to describe it. Unexplained: the deleted conversation
(`868903a5`) left only a `desktop-released.json` marker, so we cannot tell whether the title
was carried over or generated from a stale prompt.

## F-006: A session start with nothing after it, 8 seconds before the CLI session (2026-10-11)

**What we saw.** Session `86e55708` has one event: `SessionStart`, `source: startup`, folder
`C:\Users\kier\afr-test`, at 18:34:10 UTC. Its transcript file was never created. The CLI
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

## F-005: Desktop app conversations were not recorded: hooks were registered for one project only (2026-10-11)

**What we saw.** The test settings put afr's hooks in
`C:\Users\kier\afr-test\.claude\settings.json`, which applies only to sessions in that
folder. The CLI session there recorded fully (4 turns, 63 tool calls, `SessionEnd` reason
`prompt_input_exit`). Three other sessions that day produced no afr record at all:

| Session | App (transcript `entrypoint`) | Folder |
| --- | --- | --- |
| `868903a5` | `claude-desktop` | `%APPDATA%\Claude\scratch-workspaces\<ids>\scratch-2026-10-11-2aca18`, deleted by the user after one answer |
| `fcb60a58` | `claude-desktop` | started in another scratch folder, then moved to `Documents\Lucid Dreaming Program`; used 8 subagents and web fetches |
| `1a7e6e00` | `cli` | `C:\Users\kier\afr` |

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
