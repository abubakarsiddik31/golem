# ADR 0056: Loop detection breadth — results, alternation, text, and nudges

Date: 2026-11-16
Status: Accepted

## Context

ADR 0051 shipped one loop check: the same tool call, compared by
canonical arguments, failing the run before it executes. That catches
polling and retries but misses two other stuck runs: a tool that keeps
returning the same result for changing calls, and two calls alternating
A-B-A-B. Pydantic AI Harness detects these plus repeated final text, and
can nudge the model instead of stopping.

Golem's loop sees each response and its tool results, so the extra
patterns need only run-scoped state plus a way to feed a correction back
into the conversation.

## Decision

`WithLoopPolicy` configures up to four checks:

- repeated call (same name and canonical arguments),
- repeated result (same call and result fingerprint),
- alternating calls (A-B-A-B cycles),
- repeated final text (normalized case/whitespace).

Thresholds are the repetitions allowed; zero disables a check and must be
at least 2 when set. `Window` bounds call-based history (zero uses 10).
`WithLoopDetection` remains a shorthand for a whole-run repeated-call
stop, and the two options are mutually exclusive.

`LoopStop` returns `RunError{Stage: StageLoopDetected}` wrapping a
`*LoopDetectedError` with the pattern, involved tool names, count, and
threshold. Pre-execution call checks stop before the offending call
runs.

`LoopNudge` continues instead:

- A pre-execution call loop closes the requested calls with failed tool
  results saying they were not executed, appends a user-role
  course-correction, resets detection state, and continues.
- A repeated-result loop appends the nudge after the recorded results.
- A repeated-text loop appends the nudge and asks the model again.

Nudges are bounded by the existing max-iteration and usage limits. The
run's result evidence keeps the failed synthetic results and the nudge
message.

## Alternatives considered

- **Auto-nudge with no stop mode**: a model that ignores the nudge runs
  until another limit. Keeping stop as the default preserves a hard
  guarantee.
- **Nudge through a system prompt**: Golem's history system messages are
  replaced by current instructions, and a mid-run system turn has
  provider-specific behavior. User-role text is portable and lower
  authority.
- **Detect results by comparing full text**: memory-heavy and sensitive
  to formatting. A SHA-256 fingerprint of failed flag, tool name, and
  content is bounded and deterministic.
- **Sliding window for everything**: repeated-result and alternating
  checks need a window; repeated text is inherently consecutive. One
  `Window` setting covers the call-based checks.
- **Handler callback**: a callback could implement custom nudges, but it
  would need a mutation seam the runner does not expose. The typed
  error and built-in nudge cover the common policies.

## Consequences

- New exported names: `WithLoopPolicy`, `LoopPolicy`, `LoopAction`,
  `LoopPattern`, and extra `LoopDetectedError` fields; all additive.
- `runner.ToolConfig` gains a loop configuration; state is run-scoped and
  discarded with the run.
- Nudge mode can spend iterations without progress; the guide says so.
- Repeated-result loops have already executed the offending call.
- Tests cover stop and nudge for repeated results and calls, alternating
  pre-execution stop, repeated-text state, fingerprints, and validation.
