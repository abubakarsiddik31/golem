# ADR 0002: Tool execution boundary and loop policy

## Status

Accepted.

## Context

The runner must orchestrate model calls and tool executions. Two decisions
shape everything downstream: what a tool's execution boundary receives, and
how the loop terminates. Both are difficult to reverse once tools are
published.

Go's import graph constrains the first decision. The root `golem` package
owns `Agent` and `RunContext` and needs to call the runner; therefore
`tool` and `internal/runner` must not import `golem`, or the program will
not compile.

## Decision

### Tool execution boundary

`tool.Tool[Deps].Exec` receives `(ctx context.Context, deps Deps, args
json.RawMessage)` — the dependency value directly, not a `RunContext`.

- It breaks no import edge: `tool` imports only `context` and
  `encoding/json`.
- `RunContext[Deps]` remains the caller-facing carrier into `Agent.Run`.
  The runner unpacks `Deps` and passes the value to tools.
- If tools later need run metadata (run IDs, retry hints, usage), the
  options are an additive second parameter or a small shared context
  package; that change gets its own ADR when a concrete need exists.

### Loop policy

The runner (`internal/runner`) owns a strictly sequential loop:

1. Call the model with the current messages and tool specs.
2. If the response requests no tool calls, it is the final response.
3. Otherwise execute each requested call in declaration order, appending
   the assistant tool-call message and one `RoleTool` result message per
   call, then continue at 1.

- **Sequential only.** No parallel tool execution in v0; any future
  concurrency must specify goroutine ownership, cancellation, and error
  delivery per `docs/patterns.md` §6.
- **Abort on tool error.** A failing tool ends the run with a tool-stage
  error preserving the cause. Feeding failures back to the model is a
  future opt-in policy, not a default.
- **Explicit iteration limit.** Default 10 model turns per run,
  overridable. Exceeding it is a loop-stage error, not a hang.
- **Cancellation between steps.** The caller's context is checked before
  each model call and each tool execution.
- **Retries belong to the runner** as explicit policy; models and tools
  return classified errors and never retry themselves.

## Alternatives considered

- **Pass `RunContext[Deps]` to tools** by moving it into a shared package.
  Rejected for now: it creates a package for one struct, and v0 tools need
  only `Deps`.
- **Feed tool errors back to the model** so it can recover. Deferred: it
  hides failures from the application by default, which contradicts the
  foundation's explicitness rule.
- **Parallel tool execution** matching provider batching. Deferred: no
  consumer, and concurrency in core needs a documented lifecycle first.

## Consequences

- Tool authors see only `Deps`, so run metadata added later must not break
  their signatures.
- Sequential execution is slower for batches of independent calls; the
  limit and ordering make every run auditable and deterministic.
- The default limit of 10 is a policy constant owned by the core package,
  configurable per agent.
