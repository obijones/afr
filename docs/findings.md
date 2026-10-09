# Findings from testing

Behavior of AI agents and harnesses that afr testing revealed, with evidence and what it
means for afr. Newest first.

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
