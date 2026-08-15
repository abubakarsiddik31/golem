# ADR 0001: Tool-call message contract

## Status

Accepted.

## Context

Golem's `model.Message` carries a single `Content string`. To support tool calling, the normalized conversation must express three things it cannot express today:

1. An assistant response that requests one or more tool executions.
2. A result message that answers a specific tool call.
3. A request that advertises which tools exist.

Adapters for every provider will translate this contract, so changing it later is expensive. Pydantic AI's reference (see `docs/upstream-references.md`) models a full parts list per message; that generality is what we must decide against or adopt now.

## Decision

Extend `model.Message` additively rather than rewriting it as a parts list.

- `Message.ToolCalls []ToolCall` on an assistant message holds requested calls. A message has either content or tool calls, but the type does not forbid both; the runner treats tool calls as terminal for the turn.
- `ToolCall{ID, Name string; Args json.RawMessage}` identifies the call and carries provider-supplied arguments. `Args` stays raw JSON: it is untrusted model output until a tool decodes and validates it explicitly.
- `RoleTool` plus `Message.ToolCallID` and `Message.ToolName` correlate a result message with its call; `Content` carries the tool's returned text.
- `Request.ToolSpecs []ToolSpec{Name, Description string; Schema json.RawMessage}` advertises tools to the model. `Schema` is a JSON Schema document for the arguments object.

Adapters must: generate stable tool-call IDs when a provider omits them, normalize provider argument encodings (e.g. stringified JSON) to raw JSON, and omit fields they cannot fill rather than inventing values.

## Alternatives considered

- **Parts-list messages** (one message, `[]Part` with text/tool-call/tool-result parts, as Pydantic AI does). Most general and multimodal-ready, but it breaks every current call site, complicates the common text-only path, and pays now for capability (images, audio, streaming deltas) that has no consumer yet. Revisit via a new ADR when a real feature forces it; the additive fields below keep that migration to one message type.
- **Separate `ToolCallMessage` and `ToolResultMessage` types** with a message interface. Avoids unused fields but forces every consumer into type switches for the ordinary text case.

## Consequences

- Existing code keeps compiling; text-only messages are unchanged.
- Assistant messages with both content and tool calls are representable and must be interpreted by convention (tool calls win the turn), which is documented on the field.
- Correlation fields (`ToolCallID`, `ToolName`) are meaningless on non-tool messages and are ignored there.
- A future parts-list migration would absorb these fields; the serialized shapes chosen here (raw JSON arguments, string IDs) are forward-compatible with that design.
