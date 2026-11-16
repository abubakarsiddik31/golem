# ADR 0044: Planning tools — a model-owned task list

Date: 2026-11-15
Status: Accepted

## Context

Long agent runs drift: the model loses track of the goal and what is
left. Pydantic AI Harness ships `Planning`, a task list the model edits
through tools, with the plan returned on the tool-result channel so
edits do not invalidate the cached prompt prefix.

Golem has no planning primitive, and there is no natural core seam for
one: a plan is not a run event, not a history processor, and not a model
capability. It is a small, self-contained toolset over application-owned
state — the same shape as `skills`, `retrieval`, and `shell`. Putting it
in the core would enlarge the frozen surface for a tool that applications
may not want.

## Decision

A new `planning` package returns five ordinary `tool.Tool[Deps]` values:
`write_plan`, `read_plan`, `add_task`, `update_task_status`, and
`remove_task`. Every mutating tool returns the full plan as a checklist,
so the latest plan is already in the conversation and the package does
not need a per-request injection hook.

State lives behind `planning.Store`, two methods — `Load(ctx, key)` and
`Save(ctx, key, Plan)` — with `planning.Memory` as the offline
implementation. `Config.Key` derives the key from the run's dependencies,
so one store can scope plans per tenant or conversation. The package
never reads the environment, clock, or working directory.

Bad model input returns `&tool.Failed`, the existing definitive-failure
channel, so the model can correct a call without consuming a retry budget
or aborting the run. Store failures are infrastructure errors and abort
at the tool stage.

`planning.Instructions` documents the convention — one in-progress step —
but the package does not inject it. Agent instructions remain the
application's.

## Alternatives considered

- **A core capability or middleware** (Pydantic AI's capability system):
  rejected in ADR 0042 for guardrails and still rejected here. A
  planning toolset needs no loop integration.
- **Re-inject the plan before every request** (Pydantic's reminder):
  requires a per-request hook and deps-aware history processing that the
  core does not expose. Returning the plan on every mutation keeps the
  common case cache-safe; a model that loses the plan after compaction
  can call `read_plan`.
- **Automatic instruction injection**: hides an agent-prompt decision in
  a tool package. `Instructions` is exported for the caller to install.
- **An application-owned `PlanStore` only, no Memory**: every example
  and test would carry the same map-and-mutex implementation.
- **A file or SQLite store**: persistence backends stay application-side
  behind the port, following `session` and `retrieval`.

## Consequences

- New exported package with a small surface; the core module and v1 API
  are untouched.
- The plan is prompt content and not a security boundary; the guide says
  so, and high-stakes actions still re-check their own state.
- The toolset serializes its own load-mutate-save steps, but mutually
  shared stores need application-level coordination. Documented in the
  guide.
- Tests cover the full lifecycle, key isolation, recoverable input
  failures, and the infrastructure-failure path with a fake store.
