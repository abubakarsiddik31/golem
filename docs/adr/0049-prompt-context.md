# ADR 0049: Per-run prompt context

Date: 2026-11-15
Status: Accepted

## Context

A run sometimes needs context the application derives from its typed
dependencies: a remembered notebook, retrieved passages, a tenant
reminder. Today the caller can approximate this by rebuilding history or
by putting the text into instructions. Rebuilding history duplicates the
history processor and repair logic; instructions give application data
system authority it should not have, which is especially wrong for
model-written memory.

Pydantic AI adds a memory snapshot as a user-role part with the prompt.
Golem has no deps-aware, user-role pre-prompt seam. `HistoryProcessor`
runs before the request but sees only history; `InstructionsFunc` sees
deps but writes in system voice. Memory v1 shipped tools-only for this
reason (ADR 0048).

## Decision

`WithPromptContext` installs a `PromptContextFunc[Deps]`:

```go
func(ctx context.Context, runCtx RunContext[Deps]) ([]model.Message, error)
```

It is evaluated once per run after the history processor and before
input guards and request construction. Returned messages are inserted
after repaired history and before the new prompt. Every message must be
a user turn with content or parts and no tool-call, tool-result, or
thinking fields; parts pass `model.Part.Validate`. The run stamps its
identity on the messages and keeps them in `Result.Messages`. A function
error or invalid message fails the run before any model call, without a
`RunError` stage, matching the history-processor contract.

The seam is generic; memory is its first consumer.
`memory.Snapshot(ctx, cfg, deps)` returns a bounded user-role notebook
summary and `ok=false` for an empty notebook, so callers can contribute
nothing. Snapshot content is framed as data from earlier runs, not
instructions, and the caller decides whether a store failure fails the
run.

Deferred resumes evaluate the context too, so a resumed run can include
fresh context. The function is not re-evaluated within a run's tool
loop; context does not change after tool results.

## Alternatives considered

- **Rebuild history in the application**: duplicates repair, system
  stripping, and identity stamping; easy to diverge from the run's
  contract.
- **Use instructions**: gives user-derived data system authority and
  invalidates the prompt-cache benefit of stable instructions when it
  changes.
- **A history processor**: cannot see dependencies, so it cannot resolve
  a namespace or tenant.
- **Automatic callbacks on run events**: events are observations, not a
  mutation seam, and would make context order depend on observer
  registration.
- **Let context add assistant or system messages**: application code
  could forge turns. User-only keeps the seam honest.
- **A memory-specific core hook**: a general user-role seam is smaller
  and serves retrieval, tenancy, and other context needs with one
  documented contract.

## Consequences

- New exported names: `PromptContextFunc` and `WithPromptContext`,
  additive to the v1 surface. `Memory` gains `Snapshot` and snapshot
  bounds.
- One extra function call per run, before the model; no new goroutines.
- Context is part of the transcript and reaches history/UI clients.
  The guide warns against secrets and repeated snapshots.
- Input guards do not screen context; applications own validation of the
  data they inject.
- Tests cover ordering, typed deps, identity, one-call-per-run,
  validation of every rejected shape, and the no-model-call failure
  path; memory tests cover snapshot bounds, namespaces, and errors.
