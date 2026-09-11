# ADR 0028: In-tool run cancellation

## Status

Accepted.

## Context

A tool sometimes discovers mid-execution that the run should not
continue: a guarded action was vetoed by policy, a budget the tool
checks is exhausted, the user hit stop while the tool held the
context. Today the only exits are failure modes — a plain error fails
the run at the tool stage as an accident, and `&tool.Failed{}` records
a result the model then acts on. Neither says "stop the whole run,
cleanly, on this tool's authority", and neither keeps the batch's
completed evidence the way a deliberate stop should.

The parity pass and the roadmap's pre-v1.0 series call for in-tool run
cancellation. Upstream (Pydantic AI) makes `RunContext.cancel()` raise
`RunCancelled`, a catchable exception carrying the resumable history:
tool calls that already completed keep their results, calls left
without a result are repaired when the history is reused, parallel
tasks are cancelled and drained (a synchronous tool cannot be
force-stopped, so its side effects remain either way), and an external
cancellation — the environment killing the run — stays a raw
`CancelledError` rather than masquerading as a first-party stop.

Golem already has the external surface: the caller's `context`. What is
missing is the tool-side surface, and golem's tool contract has no run
handle a tool could call `Cancel()` on — `Exec` receives `ctx`, deps,
and raw args.

## Decision

A tool ends the run by returning the typed sentinel `&tool.Canceled{Reason}`.
The error channel already exists, so `Exec` keeps its signature — no
run-context object, no second interface — and the sentinel sits beside
`&tool.Failed{}` as the second deliberate, non-accidental error a tool
can return. Cancellation is first-party only: a caller's `ctx` cancel
remains a raw `context.Canceled` at the tool stage, never rewritten
into a clean stop.

The runner stops the batch deterministically, in emission order.
Executed calls before the cancelling call keep their recorded results
— successes, `&tool.Failed{}` records, and rejection feedbacks alike.
The cancelling call itself, every call after it in emission order
(same-group siblings included: the parallel group is golem's
concurrency unit, and results of work concurrent with the stop are
discarded just as upstream discards in-flight results), deferred calls
— which never produced results —, and every call in later groups,
which never start, receive the synthesized interrupted result the
output-tool and history-repair paths already use. The transcript is
provider-valid the moment the run ends, so resuming needs no repair.
A pause is discarded: if the batch also contained deferred calls, the
run ends cancelled rather than pending.

The run reports `RunError{Stage: StageCanceled}` — a new additive
stage value, because a deliberate stop is none of the failure stages —
with the source sentinel reachable through the wrap chain via
`errors.As`, and `RunError.Partial` preserving the transcript through
the last completed model turn plus the batch's recorded results, the
usage, the finish reason of the last completed turn, the activity
counts (the cancelling call executed and counts; skipped calls do
not), the cost, and the run identity. Feeding `Partial.Messages` to
`RunWithHistory` resumes the conversation.

Events stay deterministic and additive: the cancelling call's
`EventToolEnd` carries the sentinel as its error, a new
`EventCanceled` marker follows it (the boundary, like
`EventOutputRejected`), calls that never ran emit nothing, and no
event kind changes meaning.

Sub-agents cancel their parents: a delegated run that ends cancelled
returns its `RunError` through `AsTool`'s error, and the parent's
batch processing finds the sentinel through the chain, ending the
parent run cancelled too. An approved deferred re-run that returns the
sentinel fails the resume run at the cancellation stage before any
model call.

## Consequences

The public surface grows by `tool.Canceled`, the `StageCanceled`
constant, and `EventCanceled` — all additive below v1. A cancelled run
is an error return, matching upstream's exception and Go's
ctx-cancellation convention: decode never runs, so there is no output
to return, and every caller already handles `(Result, error)`. Force-stop semantics are explicitly out: the runner waits for the executing
group (a non-cooperative tool cannot be stopped, and its side effects
remain), skips only work that had not started — the same honesty
upstream documents. Bounding who may cancel is the application's job:
any tool that can return the sentinel can end the run, so the sentinel
belongs in tools the application trusts with that authority, and
reviewers should read a tool returning it as part of the tool's
contract.
