# ADR 0013: Agent as a tool

## Status

Accepted.

## Context

Single agents accumulate responsibilities until their tool lists and
instructions blur. Applications need delegation: one agent handing a
subtask to a specialist agent and using its typed result, while history,
usage bounds, and evidence stay owned by each run. Golem already has the
two halves — a typed agent run and a typed tool contract — but nothing
composes them.

## Decision

Delegation is a constructor, not a new execution concept: a method on
`Agent[Deps, Output]` returns an ordinary `tool.Tool[Deps]`, so a
sub-agent registers with `WithTools` like any other tool and the
existing loop, budgets, and evidence apply unchanged.

The tool's arguments are a single required `prompt` string — the
sub-agent sees nothing else of the delegating conversation. The
sub-agent runs with `Run` and the delegating run's dependency value;
both agents must therefore share the `Deps` type. A successful output is
rendered to text for the model: string outputs pass through unquoted,
every other type is JSON-encoded, and `WithAgentResult` replaces the
renderer.

Malformed or empty `prompt` arguments are rejected with
`*model.ModelRetry`, so correction uses the existing tool retry budget.
Every other sub-agent failure propagates as a tool-stage error with the
inner `RunError` preserved in the chain; cancellation propagates raw.
Nothing is retried or softened implicitly: a failed delegation is a
failed run unless the caller opts into correction.

## Alternatives considered

A dedicated delegation or handoff abstraction in the runner would add a
second orchestration path that duplicates tool execution policy. Mapping
inner failures to `ModelRetry` by default would hide hard failures from
the delegating application. A dependency-mapping constructor (outer
deps to inner deps of a different type) is deliberately absent: it is
additive later, and the shared-deps form covers the delegation pattern
without another exported concept to freeze.

## Consequences

A sub-agent's conversation is not part of the delegating run's
evidence — only the rendered result is; callers capture the inner
`Result` through a custom renderer when they need it. Nothing prevents
an agent from being registered as its own tool; the resulting recursion
is bounded only by iteration and usage limits, and the guide says so.
