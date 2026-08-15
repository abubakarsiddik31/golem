# ADR 0005: Message history

## Status

Accepted.

## Context

A run's `Result.Messages` already returns the full normalized conversation
evidence, but there is no way to feed it back: `Run` always builds a fresh
`[system?, user]` request. Chat applications, follow-up questions, and
human-in-the-loop flows all need conversation continuation.

Upstream, Pydantic AI treats history as a first-class value: a
`message_history` parameter on every run method, serialization helpers for
persistence, and no session object or storage backend. That design matches
Golem's explicitness rules and translates without Python-specific
machinery.

## Decision

### History is a plain value

`Agent.RunWithHistory(ctx, runCtx, history, prompt)` continues a
conversation. History is a `[]model.Message` supplied by the caller —
typically the `Result.Messages` of a previous run. There is no session
type, no run or conversation IDs, and no storage backend: persistence is
the application's job, and `encoding/json` is the codec.

### Instructions are re-evaluated, never inherited

The request for a continued run is:

```
[system: current agent instructions, if set]
+ history with every RoleSystem message removed
+ [user: prompt]
```

The current agent's instructions always govern the request. System
messages found in the supplied history are dropped rather than duplicated:
an application can change an agent's instructions mid-conversation without
stacking stale guidance, and no run ever carries two system prompts.
`RoleUser`, `RoleAssistant`, and `RoleTool` messages pass through
untouched, in order.

### The result chains

The continued run's `Result.Messages` is the full reconstructed
conversation — the supplied turns plus this run's exchange — so feeding one
result into the next run composes multi-turn conversations naturally.

### The serialized shape is a durable contract

`model.Message` and `model.ToolCall` carry JSON tags (lowerCamel field
names, `omitempty` on optional fields). Once applications persist
conversations, that shape can only grow additively: new fields must be
optional, and existing names and meanings must not change. `json.RawMessage`
arguments round-trip byte-exact. There is deliberately no
`MarshalMessages`/`UnmarshalMessages` wrapper — `encoding/json` already is
the stable API.

## Alternatives considered

- **A session object with built-in persistence.** Rejected: implicit state
  and a storage dependency contradict the foundation's explicitness rule;
  applications differ wildly in where conversations live.
- **Keep system messages from supplied history.** Rejected: duplicates
  guidance when the same agent continues a run, and leaves ambiguous which
  instructions govern when they differ.
- **Inject instructions only when history has none.** Rejected: the
  conditional makes the request shape depend on history contents; the
  deterministic replace rule is easier to reason about and document.
- **Serialization wrapper functions.** Rejected for now: no caller need
  that `encoding/json` does not meet; a wrapper is easy to add later
  without breaking anyone.
- **Sanitizing untrusted history** (upstream's `sanitize_messages`).
  Deferred: no consumer yet. Adapters normalize provider encodings at their
  boundary; application-supplied history is the application's data-quality
  responsibility.
- **Auto-repairing malformed histories** (orphaned tool calls, and the
  like). Deferred: no consumer, and silent repair hides caller bugs.

## Consequences

- Multi-turn state lives entirely in caller hands: trivially testable,
  trivially persisted, no framework lock-in.
- Changing an agent's instructions affects the next continued run
  immediately — documented, intended behavior.
- The JSON shape becomes a compatibility surface; future ADRs must treat
  it as additive-only.
- Providers that reject malformed histories (e.g. a tool result without
  its requesting assistant call) surface that error as a model-stage
  failure rather than a framework repair.
