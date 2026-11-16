# ADR 0048: Memory tools — a persistent notebook behind a store port

Date: 2026-11-15
Status: Accepted

## Context

Pydantic AI Harness ships a memory capability: a Markdown notebook the
model edits through tools, namespaced per user, with a bounded snapshot
injected into the first request of a run. Golem has no long-term memory
primitive. Sessions persist a conversation; retrieval searches a corpus;
neither stores durable, model-owned notes across conversations.

The module's constraints still apply: standard library only, no
persistence layer in the core, additive exported surface, and
application-owned backends behind a small port. Golem also has no
per-run, deps-aware, user-role pre-prompt seam, so Pydantic's automatic
snapshot cannot be reproduced without new core surface.

## Decision

A new `memory` package owns one ordinary toolset over a small
`memory.Store` port:

```go
List(ctx, namespace) ([]string, error)
Read(ctx, namespace, path) (File, error)
Write(ctx, namespace, path, content) error
Delete(ctx, namespace, path) error
```

`File` is `Path` and `Content`. `Memory` is the in-process
implementation; file, SQLite, and Postgres backends stay application-side
behind the port. `Config.Namespace` resolves the namespace from the
run's typed dependencies, never from tool arguments.

Five tools cover the notebook: `list_memory`, `read_memory`,
`write_memory`, `search_memory`, and `delete_memory`. `write_memory`
supports append (default), whole-file replace, and a unique-fragment
edit. `MEMORY.md` is the main file and cannot be deleted. Paths and
namespaces are validated to prevent traversal; file, scan, result, and
character bounds are enforced before any store call.

V1 is tools-only: no automatic snapshot injection. The model fetches
memory on demand. A follow-up will add an additive per-run prompt-context
seam and a `memory.Snapshot` helper so applications can opt into
Pydantic-style recall without making untrusted memory part of the
system prompt.

## Alternatives considered

- **Inject a snapshot now** (Pydantic's default): requires a deps-aware
  user-role pre-prompt hook the core does not expose. Adding it inside
  the memory package would either misuse `InstructionsFunc` (system
  voice, too much authority for model-written text) or require a
  `HistoryProcessor` that cannot see dependencies. Deferred to its own
  ADR.
- **Reuse `session.Store`**: conversations are message slices keyed by
  conversation ID, not named Markdown files. Reusing it would overload
  two different lifecycles.
- **Reuse `retrieval.Index`**: that is an embedding index, not mutable
  note storage. Memory's literal search is intentionally simpler and
  needs no embedding model.
- **Whole-notebook `Load`/`Save`**: simpler, but every read or search
  would transfer every file and every write could clobber unrelated
  notes.
- **Compare-and-swap and idempotency receipts**: Golem tools receive
  deps and arguments, not run or call identity, so a store cannot derive
  an operation key without new tool surface. V1 documents last-write-wins
  and a single writer per namespace.
- **A `FileStore` in the package**: useful, but it adds path, symlink,
  atomic-write, and workspace-permission policy. Applications can
  implement the four-method port over files; a bundled store can land
  later on demand.

## Consequences

- New package and exported surface, additive to v1; the core module and
  the frozen API are untouched.
- The notebook is untrusted, model-written content that persists across
  runs. The guide says so and points applications at their own guards;
  prompt injection is not solved by namespacing or path validation.
- Search is literal, linear, and bounded; semantic ranking stays with
  `retrieval`.
- The toolset serializes read-modify-write within one instance but not
  across instances sharing a store. No CAS or replay protection in v1.
- Tests cover the lifecycle, append/replace/edit, path and namespace
  validation, size and search bounds, and infrastructure failure. The
  example runs offline against a scripted model.
