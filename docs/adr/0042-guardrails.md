# ADR 0042: Guardrails at the run's boundaries

Date: 2026-11-08
Status: Accepted

## Decision

A new `guard` package provides typed guard functions the run calls at
three boundaries: the user prompt before the first model call, each
tool call's arguments before execution and each tool result before the
model sees it, and the final assistant text before decoding. A guard
returns a `guard.Result` whose action is `Allow`, `Block`, `Replace`,
or `Retry`; chains run in registration order and a `Replace` threads
the substituted value into the guards that follow. Blocks are graceful
by default — a blocked prompt becomes a synthesized refusal decoded
through the normal path, a blocked tool call or result becomes a
synthetic tool message the model can read and route around — and an
output block fails the run with `RunError{Stage: StagePolicy}` wrapping
`*guard.Blocked`. A `Retry` verdict is valid only for tool arguments,
where it rides the existing `WithToolRetries` feedback budget. A
verdict whose action is not valid at its boundary is treated as
fail-closed: the value is blocked with a reason that names the misuse.

Wiring is four option functions — `WithInputGuards`, `WithToolGuards`,
`WithResultGuards`, `WithOutputGuards` — all additive to the frozen v1
surface; the zero value runs no guards.

## Alternatives considered

- **A capability/middleware framework** (Pydantic AI V2's Capability,
  eino's AgentMiddleware): one seam that also carries instructions,
  tools, and model settings. Rejected for now — golem's extension
  points are separate by design, and this ADR adds exactly one concern.
  Revisiting a capability primitive remains a post-v1 option if the
  seams proliferate.
- **Guards as tool wrappers** (wrap every `tool.Tool` at registration):
  cannot see the run, cannot cover input/output, and pushes ordering
  onto the caller. Rejected.
- **Deps-aware guard signatures** (`InputGuard[Deps]`): guards that
  see the run context need the root package's `RunContext`, which
  would make `guard` import `golem` while `golem` imports `guard`.
  Guards are closures and can capture application state; deps-aware
  signatures stay a deferred option if real demand appears.

## Consequences

- The runner gains two config fields and a resolve step per tool call;
  guard-blocked calls never execute and do not count toward
  `ToolCalls` activity, but still emit their start/end events so event
  streams stay complete.
- Output guards screen text-mode output only; output-tool mode is
  screened by result guards and the decoder instead. Documented, not
  hidden.
- The new `StagePolicy` stage is an additive `Stage` value; existing
  stages are untouched.
- Blocks are guardrails against accidents, not a security boundary:
  the model sees refusal text and can adapt. Applications needing
  isolation still own it (see ADR 0015 and the command-execution
  guide).
- Guard application is deterministic, stdlib-only, and unit-tested in
  the `guard` package; the runner and agent tests pin the wiring at
  each boundary with fake models and fake tools.
