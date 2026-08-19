# ADR 0012: Per-tool retry and timeout policy

## Status

Accepted. Renumbered from a colliding 0008 (the streaming-port decision keeps
that number); content is unchanged.

## Context

An agent-wide correction budget makes one unreliable tool consume another
tool's opportunity to correct a model-produced argument. Unbounded tool calls
can also hold an otherwise cancellable run indefinitely.

## Decision

Tool correction counts are tracked independently by tool name. The existing
`WithToolRetries` value is the default; `Tool.MaxRetries` overrides it when
set, including an explicit zero. Tool timeouts use the same context passed to
`Tool.Exec`: `WithToolTimeout` is the default and non-zero `Tool.Timeout`
overrides it. No goroutine is used to race or abandon non-cooperative work.

## Alternatives considered

Keep one global retry counter, or silently replay tool executions after a
timeout. Both hide per-tool policy and risk repeating application side effects.

## Consequences

Tool authors must honor contexts. Existing tools retain their old behavior:
they inherit the agent retry default and have no deadline unless configured.
