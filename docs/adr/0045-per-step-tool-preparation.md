# ADR 0045: Per-step tool preparation

Date: 2026-11-15
Status: Accepted

## Context

Tool registration is static: `WithTools` fixes the catalog for every
request of every run. Applications often need advertisement to vary
without unregistering anything — a reader sees `read_file` but not
`delete_file`, a later step hides a discovery tool, a caller's role
narrows the surface for one run. Pydantic AI exposes this as
`PrepareTools`, a function that filters or modifies tool definitions per
step.

The loop already computes the advertised specs per turn in
`advertisedSpecs`, after deferred-tool discovery. A hook there is one
closure and no new concurrency: the function runs synchronously before
the request is built, with the caller's context.

## Decision

`golem.WithPrepareTools(fn)` installs a `PrepareToolsFunc[Deps]` —
`func(ctx, RunContext[Deps], []model.ToolSpec) ([]model.ToolSpec, error)`
— that filters or rewrites the specs advertised on every model request.
It runs after deferred-tool discovery, so it sees the final set for the
turn, and before the pre-send usage check, so a token estimate prices
what will actually be sent.

The runner enforces the contract before the request:

- every returned name must already be advertised this turn;
- names are unique;
- every schema is non-empty valid JSON;
- the configured output tool remains when tool-mode output is active.

A violation, or a non-nil error, fails the run before the model call.
Preparation cannot add a callable tool because dispatch is declared-
tool-based: a spec the loop does not know would send a request whose
call can never run. Rewriting descriptions and schemas is allowed; the
model reads those.

## Alternatives considered

- **Tool wrappers at registration** (wrap each `tool.Tool` to hide it):
  cannot vary per step without mutable state, and hides the decision
  from the runner that advertises specs.
- **`WithToolChoice`**: already constrains the run to one registered
  tool, but it is one fixed name, not a per-step function, and it
  cannot rewrite metadata.
- **A capability/middleware framework**: rejected in ADR 0042; this is
  one concern at one existing seam, not a general interception layer.
- **Allowing prepared specs to add tools**: would require preparation
  to return executable tools, not specs, and a second registration
  path. Applications that need a dynamic catalog already have
  `WithDeferredTools` and agent-as-a-tool.
- **Silently ignoring invalid output**: rejected. A request that cannot
  dispatch its own advertised calls is a bug; failing before the model
  call preserves the evidence and cost.

## Consequences

- New exported names: `PrepareToolsFunc` and `WithPrepareTools`, both
  additive to the v1 surface.
- `runner.ToolConfig` gains one field; both plain and streaming loops
  share the hook through `execute`.
- Preparation errors classify at the model stage, alongside other
  request-build failures, and carry the partial evidence accumulated so
  far.
- Removing the runner-owned `search_tools` spec disables discovery for
  that request; deferred tools stay declared but unreachable. Documented
  in the tools guide.
- Tests pin per-step filtering, typed deps visibility, description
  rewriting, every validation failure, and the missing-output-tool
  path, each proving no model call happens.
