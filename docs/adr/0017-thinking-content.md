# ADR 0017: Thinking content

## Status

Accepted.

## Context

Reasoning models are now the default operating mode of every frontier
provider, and several providers emit reasoning without being asked:
Claude Sonnet 5 and Opus 5 run adaptive thinking when the request omits
`thinking` entirely, and Gemini 2.5+ models think by default. The
reasoning arrives as structured content, not prose: Anthropic
`thinking` blocks with a verifiable `signature`, Bedrock
`reasoningContent` blocks, Gemini parts flagged `thought` with
`thoughtSignature` values, and a non-standard `reasoning_content` field
on OpenAI-compatible chat endpoints.

Dropping that content is not a cosmetic loss. The signatures are the
providers' proof that reasoning content is unmodified, and they are
required on later turns: Anthropic tool-use flows reject history whose
assistant turns lost their thinking blocks, Gemini 3 returns a 400 when
a required thought signature is not returned, and Bedrock
`reasoningContent` — text, signature, or the encrypted `redactedContent`
payload — must be replayed unchanged. Golem's adapters currently skip
all of these as unknown fields, so multi-turn tool use against
thinking-by-default models silently degrades or fails.

Two verified provider constraints shape the design. First, adaptive
thinking can interleave: an assistant turn may carry several thinking
blocks around text and tool calls. Second, on OpenAI's chat-completions
surface the newest reasoning models reject `tools` combined with
`reasoning_effort` outright (the Responses API is their recommended
path, which Golem does not speak), while OpenAI-compatible endpoints
return `reasoning_content` only as a response field.

The message JSON is a durable, additive-only contract (ADR 0011); any
change must leave existing encodings byte-identical and decode older
history unchanged.

## Decision

Represent reasoning as an assistant-message field, parallel to ADR
0011's parts decision for user content.

`model.Message` gains `Thinking []ThinkingBlock`. `ThinkingBlock` is a
flat struct — `Text` plus the provider's opaque `Signature`, or
`Redacted` for the encrypted payload — with constructors
`model.ThinkingText`, `model.ThinkingSigned`, and
`model.ThinkingRedacted`, and a `Validate` the agent runs on history at
run start. Blocks keep provider order, so an interleaved turn's
signatures round-trip one-to-one.

`model.ToolCall` gains `Signature`: Gemini attaches
`thoughtSignature` to the function-call part itself, so the call is the
only faithful carrier.

`model.Delta` gains `Thinking []ThinkingDelta`
(`Index`, `Text`, `Signature`), mirroring `ToolCallDelta`. Deltas carry
no stability promise, so this stays additive by construction.

Enablement stays where ADR 0011 put sampling controls: adapter
configuration, not a request-level settings concept. Anthropic and
Bedrock get a typed `ThinkingConfig` (`Adaptive`, `BudgetTokens`,
`Disabled`) plus an `Effort` control, mapped to `thinking` /
`output_config.effort` — Bedrock's rides
`additionalModelRequestFields` and the top-level `outputConfig`. Gemini
gets `ThinkingConfig` (`IncludeThoughts`, `Budget`, `Level`) mapped to
`generationConfig.thinkingConfig`. OpenAI and Azure get
`ReasoningEffort`, mapped to `reasoning_effort`.

Adapter behavior:

- **anthropic:** parse `thinking` and `redacted_thinking` blocks and
  their streaming deltas; emit stored blocks (then text, then tool
  calls) on assistant turns; the API rejects forced tool choice while
  thinking is on, which surfaces as the provider's own error.
- **bedrock:** parse `reasoningContent` blocks and stream deltas; replay
  them unchanged on assistant turns.
- **gemini:** parse `thought` parts and `thoughtSignature` values;
  replay thought parts and attach stored signatures to function-call
  parts, honoring the provider's all-or-nothing consistency rule.
- **openai / azure:** capture `reasoning_content` from responses and
  stream deltas; send `reasoning_effort` when configured. Thinking is
  capture-only and is never replayed: OpenAI ignores the field on
  assistant turns and DeepSeek-style endpoints reject it, so no single
  replay rule exists on this wire.

## Alternatives considered

- **Generalize `Part` with a thinking kind.** Parts are documented as
  user-message content and carry no text field; assistant reasoning
  would reshape a contract that ADR 0011 pinned, for no gain over a
  dedicated field.
- **One flat `Thinking string` per message.** Simpler, but it cannot
  carry per-block signatures, which are the whole point: adaptive
  thinking interleaves, and Bedrock requires either signature
  fidelity or a merge with an explicit signature-revalidation caveat.
- **A provider-neutral request-level thinking setting.** A unified
  `thinking` knob (as Pydantic AI's V2 capability does) needs the
  request-settings concept Golem has so far rejected — ADR 0011 left
  sampling controls on adapter Configs — and provider effort ladders
  differ enough that the neutral surface would be a lowest common
  denominator. Revisit with a real request-settings ADR if adapter
  Configs accumulate.
- **OpenAI Responses API support** to unlock reasoning content on
  OpenAI directly. A separate adapter surface, not a field on this one;
  deferred until a feature asks for it.

## Consequences

- Persisted history gains `thinking` (and `signature` on tool calls)
  only where reasoning exists; older history decodes unchanged and
  text-only messages encode byte-identically (pinned by tests).
- The runner loop needs no change: assistant messages already ride
  through as evidence, so replay is history preservation.
- Testmodel replays thinking fragments and clones them with the rest of
  recorded evidence.
- Reasoning text is model-produced content: it is evidence, not
  instructions, and the agent's prompt-assembly path never treats it as
  such.
- OpenAI and Azure users see reasoning only from endpoints that return
  `reasoning_content`; OpenAI's own chat surface reports reasoning
  tokens in usage but no reasoning text. The guide says so.
