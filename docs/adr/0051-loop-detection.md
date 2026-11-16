# ADR 0051: Repeated tool-call detection

Date: 2026-11-16
Status: Accepted

## Context

Autonomous runs can get stuck requesting the same tool call: polling a
job that never finishes, re-reading a missing file, retrying an
unchanged query. Every repeat costs a model request and, for
side-effecting tools, can duplicate work. Pydantic AI Harness detects
repeated calls, results, alternations, and text, then nudges or fails.

Golem has turn limits and usage limits, but neither notices that the
model is repeating one call. A core check is small: the loop already
sees every requested call and its arguments, and a run-scoped map is
enough to count them without a new extension point.

## Decision

`WithLoopDetection(maxRepeats)` sets a whole-run bound on identical tool
calls, compared by tool name and canonical JSON arguments. Go's
`encoding/json` canonicalizes object key order; malformed arguments fall
back to raw bytes so a repeat is not hidden. The check runs at tool
dispatch, before any call in that batch executes. When a call would
exceed the bound, the run fails with `RunError{Stage:
StageLoopDetected}` wrapping `*LoopDetectedError`, which carries the
tool name, raw arguments, count, and threshold.

Zero disables detection; negative values fail `New`. The failing call
never executes, so the detector cannot itself duplicate a side effect.
Partial evidence ends at the assistant turn that requested the call and
is resumable with different arguments.

## Alternatives considered

- **Sliding window** (Pydantic's `window`): more forgiving for long runs
  but adds configuration and makes the bound harder to explain. A
  whole-run count is predictable and sufficient for stuck agents.
- **Compare results too**: would catch polling tools that keep returning
  the same status, but requires retaining result fingerprints and
  interacting with guards and output limits. Deferred.
- **Nudge instead of stop**: a model that ignores the nudge keeps going.
  Stopping is the hard guarantee; a future nudge mode can layer on.
- **A handler callback**: adds an extension point for one policy. The
  typed error is enough for an application to decide what to do next.
- **Detect in a `guard.ToolGuard`**: guards are per-call and stateless;
  a guard would need external mutable state, and the runner is the
  natural owner of run-scoped counts.

## Consequences

- New exported names: `WithLoopDetection`, `LoopDetectedError`, and the
  `StageLoopDetected` stage value; all additive.
- `runner.ToolConfig` gains one field and one run-scoped map. No
  goroutines, provider calls, or persisted state.
- Legitimate repeated calls need a higher bound or changing arguments;
  the guide documents this.
- Calls are counted when requested, so blocked, rejected, and deferred
  calls count too.
- Tests cover the stop-before-execute guarantee, canonical key order,
  distinct and bounded calls, disabled behavior, malformed arguments,
  negative configuration, and the partial-evidence path.
