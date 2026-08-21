# ADR 0014: Run event stream

## Status

Accepted.

## Context

A run's evidence exists only after it ends: `Result.Messages` is a
complete, ordered record, but nothing observes a run while it executes.
Applications building logs, traces, or progress reporting have no
provider-neutral hook, so "observable execution" stops at post-hoc
inspection. The contract added here will be hard to change once
observers exist, so its shape is decided now.

## Decision

Observation is a synchronous callback, not a channel or interface:
`WithRunEvents(onEvent func(RunEvent))` registers one observer per
agent, and the run calls it in deterministic execution order — no
goroutines are introduced, and an observer that must stop the run
cancels the run context rather than failing the callback. The callback
returns nothing: observation can never fail a run.

`RunEvent` is a single struct with a `Kind` and the fields that kind
carries, so later kinds and fields are additive. The contract lives in
`internal/runner` — the loop that defines execution order — and the
root package re-exports it as `golem.RunEvent` aliases. The initial
kinds:

- `model_start` / `model_end` per provider call attempt, carrying the
  turn index, 1-based attempt number, and on end the attempt's usage
  and error. Retried attempts are individually observable.
- `tool_start` / `tool_end` per tool execution, carrying the call ID,
  tool name, raw arguments, and on end the result text or error. A
  correction rejection arrives as a `tool_end` whose error is a
  `*model.ModelRetry`.
- `output_rejected`, emitted by the agent when a decoder rejection
  starts a correction round. Turn indexes restart each correction
  round; this event marks the boundary.

Parallel tool groups stay deterministic: starts are emitted in model
emission order before the group runs, ends in the same order after it
completes — matching the result-evidence order rule.

## Alternatives considered

A channel would force buffer and drain policy on every caller and
invite dropped events. An `error`-returning callback would need a new
failure stage for a concern that is not execution. Emitting only
completed-turn events would hide retries — the main transient behavior
observers want. Defining the struct in the root package would force the
runner to import upward or duplicate the contract.

## Consequences

Observers must be quick and non-blocking; they run inline with model
and tool execution. Event order is a compatibility promise: tests
assert exact sequences, and any future kind must fit the deterministic
ordering rules. Streaming runs emit the same events alongside deltas,
with a model turn's events bracketing its fragments.
