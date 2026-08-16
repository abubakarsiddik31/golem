# Changelog

## v0.1.1 — 2026-08-16

Provider coverage and documentation infrastructure.

- **Google Gemini adapter.** Native `generateContent` wire format with
  `systemInstruction`, function calling (adapter-generated stable call
  IDs — the wire carries none), `responseSchema` structured output, and
  SSE streaming.
- **Azure OpenAI adapter.** OpenAI chat-completions wire over deployment
  URLs with the `api-key` header and an explicit, required `api-version`;
  strict `response_format` structured output and SSE streaming included.
- **AWS Bedrock adapter.** Converse API with AWS Signature Version 4
  request signing implemented in the standard library, pinned in tests to
  AWS's documented signing example. Structured output maps to the
  Converse `outputConfig` json_schema format. Streaming is not
  implemented yet (AWS binary event-stream framing).
- **Documentation.** Feature guides under `docs/guides/` are the source
  of truth (written from a shared template), runnable examples under
  `examples/` cover each capability, the README indexes both, and the
  contributor rules require keeping all three in sync. The providers
  guide lists twelve OpenAI-compatible services reachable through the
  openai adapter's `BaseURL`.

## v0.1.0 — 2026-08-16

Initial tagged release: the core agent execution contract, two provider
adapters, and streaming.

- **Typed agents.** `golem.New[Deps, Output]` with static and per-run
  instructions, explicit tools, iteration and attempt bounds, and a
  decoder boundary where model output becomes application data.
- **Evidence-preserving runs.** Every message and tool-call exchange is
  retained in execution order in `Result.Messages`, which serialize to
  stable, additive-only JSON for persisted conversations
  (`RunWithHistory` continues them).
- **Self-correction.** Output and tool rejections (`model.ModelRetry`)
  feed back to the model under configurable budgets
  (`WithOutputRetries`, `WithToolRetries`).
- **Retries with policy.** Opt-in model-call retries with classified
  errors (`model.RetryableError`), exponential backoff, and
  cancellation that always wins (`WithMaxAttempts`, `WithRetryBackoff`).
- **Streaming.** Optional `model.StreamingModel` capability, SSE
  streaming in both adapters, and agent-level `RunStream` runs that
  forward every fragment while producing the identical `Result`.
- **Structured output.** `WithOutputSchema` declares the expected answer
  shape — mapped to OpenAI `response_format` and Anthropic
  `output_config` — with `golem.DecodeJSON` decoding and rejecting
  correctable responses.
- **Providers.** OpenAI-compatible chat-completions adapter (serves
  Groq, OpenRouter, DeepSeek, Together, Ollama, vLLM) and an Anthropic
  Messages adapter; both stdlib-only with typed, retry-classified
  errors.
- **Governance.** `WithUsageLimit` bounds a run's token consumption
  across turns, retries, and corrections, failing at the `usage` stage
  with a typed `UsageLimitError`.
