# Thinking

## Purpose

Let agents use reasoning models properly: request thinking from the
provider, keep the reasoning (and its verification signatures) in the
run evidence, and replay it on later turns so multi-step conversations
verify.

## When to use

Use it when a model reasons — Claude with adaptive thinking, Gemini
thinking models, o-series and GPT-5-family reasoning models, or
OpenAI-compatible endpoints that return a `reasoning_content` field.
Thinking improves multi-step tasks and tool use; skip it for short
single-turn answers where latency matters more than accuracy.

## How it works

Thinking is enabled per adapter at construction, next to the other
sampling controls. When a response carries reasoning, the assistant
message in the run result carries it as `Thinking` blocks in provider
order, alongside the text in `Content` and the calls in `ToolCalls`.
Each block holds either visible reasoning text with the provider's
opaque `Signature`, or the provider's encrypted `Redacted` payload.

The signatures are the point: providers verify that reasoning content
round-trips unmodified. Golem's history handling — `RunWithHistory`,
the runner's evidence, and history processors — preserves blocks and
signatures as-is, so adapters can replay them and later turns verify.
History validation rejects thinking on non-assistant messages and
malformed blocks before any model call.

Streaming reports reasoning as it arrives: deltas carry `Thinking`
fragments before the text content of the same response, and the final
result is the same as a non-streamed run.

Provider mapping:

- **Anthropic** — `Thinking` (`Adaptive`, `BudgetTokens`, or
  `Disabled`) and `Effort` map to the `thinking` and `effort` request
  fields. Adaptive thinking is the modern form (Claude Opus 4.6+);
  older models take a token budget. Note that Claude Sonnet 5 and
  Opus 5 think by default: omitting the config selects adaptive
  thinking, so use `Disabled` to turn it off.
- **Bedrock** — the same `Thinking` and `Effort` controls map to
  `additionalModelRequestFields` and `outputConfig.effort` on the
  Converse API. Reasoning replays as `reasoningContent` blocks.
- **Gemini** — `Thinking` (`IncludeThoughts`, `Budget`, or `Level`)
  maps to `generationConfig.thinkingConfig`. Thought summaries become
  thinking blocks; `thoughtSignature` values ride both the blocks and
  requested tool calls, and Gemini 3 rejects later turns that drop a
  required signature.
- **OpenAI / Azure** — `ReasoningEffort` maps to `reasoning_effort`.
  Reasoning is capture-only: endpoints that return the non-standard
  `reasoning_content` field populate the blocks; nothing is replayed,
  because chat-completions endpoints disagree on replay (OpenAI ignores
  it, DeepSeek-style APIs reject it).

## Example

Run the thinking example — it asks a reasoning question and prints the
reasoning blocks and their signatures from the run result:

```bash
ANTHROPIC_API_KEY=sk-... go run ./examples/thinking
```

The essential path:

```go
client, _ := anthropic.New(anthropic.Config{
    APIKey:    apiKey,
    Model:     "claude-sonnet-4-5",
    MaxTokens: 2048,
    Thinking:  &anthropic.ThinkingConfig{Adaptive: true},
})
// ... agent.Run as usual ...
last := result.Messages[len(result.Messages)-1]
for _, block := range last.Thinking {
    if block.Redacted != "" {
        fmt.Println("[redacted reasoning]")
        continue
    }
    fmt.Println(block.Text)
}
```

## API surface

- `model.ThinkingBlock` — one reasoning block: `Text`, `Signature`,
  `Redacted`.
- `model.ThinkingText(text)` / `model.ThinkingSigned(text, signature)` /
  `model.ThinkingRedacted(data)` — block constructors.
- `(*model.ThinkingBlock).Validate()` — boundary check the agent runs
  on history.
- `model.Message.Thinking` — the ordered blocks of an assistant message.
- `model.ToolCall.Signature` — provider reasoning evidence bound to a
  tool call (Gemini's `thoughtSignature`).
- `model.Delta.Thinking` / `model.ThinkingDelta` — streamed reasoning
  fragments (`Index`, `Text`, `Signature`).
- `anthropic.ThinkingConfig`, `bedrock.ThinkingConfig`,
  `gemini.ThinkingConfig` — per-adapter enablement, plus
  `Effort` on Anthropic and Bedrock and `ReasoningEffort` on OpenAI
  and Azure.

## Gotchas

- **Thinking costs output tokens.** Reasoning is billed as output; a
  small `MaxTokens` can truncate the answer before it starts. Anthropic
  reports thinking tokens inside `output_tokens`.
- **Thinking-by-default models.** Claude Sonnet 5 and Opus 5 run
  adaptive thinking when the request omits `thinking`; only
  `Disabled` turns it off. Budget-style configs are rejected by those
  models.
- **Signatures are opaque and immutable.** Never edit or drop them;
  providers verify the reasoning chain through them. Persisted history
  JSON carries them automatically.
- **OpenAI chat completions limits.** The newest OpenAI reasoning
  models reject `tools` together with `reasoning_effort` on the chat
  surface, and OpenAI's own chat endpoint never returns reasoning text
  — only compatible endpoints do. Golem does not speak the Responses
  API yet.
- **Gemini redacted reasoning cannot replay.** Gemini has no encrypted
  reasoning part; a block that somehow arrives redacted is skipped when
  the history is re-sent, while its evidence stays in the history.
- Anthropic rejects forced tool choice while thinking is enabled; the
  provider's error surfaces as an `*anthropic.APIError`. See
  [ADR 0017](../adr/0017-thinking-content.md) for the deciding details.
