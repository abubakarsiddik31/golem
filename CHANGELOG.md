# Changelog

## Unreleased

### Added

- **Run events.** `WithRunEvents` registers an observer invoked for every
  observable point of each run: provider call attempts — retried attempts
  included —, tool executions, and decoder correction boundaries. Events
  are a typed struct delivered synchronously in deterministic execution
  order (parallel tool groups emit starts in model emission order before
  the group runs, ends in the same order after), the observer can never
  fail a run, and `Run`, history, and streaming variants emit the same
  events. `Result.Messages` remains the canonical record (ADR 0014).
- **Request tuning.** Every provider Config gains optional sampling and
  length controls — `Temperature`, `TopP`, and `MaxTokens` (the latter
  new on openai, azure, and gemini) — validated against each provider's
  documented ranges at construction and mapped to its native encoding:
  top-level fields on the chat-completions and Messages APIs,
  `generationConfig` on Gemini, and `inferenceConfig` on Bedrock. Unset
  controls stay off the wire; a nil sampling field leaves the provider
  default, and the new `providers.Ptr` builds the optional values,
  including a temperature of 0.
- **Agent delegation.** `Agent.AsTool` exposes one agent as a tool of
  another: the delegating model passes a prompt, the sub-agent runs with
  the delegating run's dependency value, and its typed output is rendered
  to text — strings as-is, other types JSON-encoded, `WithAgentResult`
  to replace the rendering. Missing or malformed prompt arguments
  correct through the tool retry budget; every other sub-agent failure
  surfaces at the tool stage with the inner `RunError` preserved, and
  cancellation propagates unwrapped (ADR 0013).

## v0.3.1 — 2026-08-21

Patch release: the open-source launch essentials and a fail-fast
provider fix.

### Added

- **Open-source community files.** MIT license, contributing guide, security
  policy, code of conduct, and GitHub issue and pull-request templates.

### Fixed

- **Gemini rejects output schemas combined with tool declarations before
  the request.** The GenerateContent API answers 400 INVALID_ARGUMENT when
  JSON response mode is combined with function calling, so such runs always
  failed mid-flight with a cryptic provider error. The adapter now fails
  fast at the encode stage with a typed error pointing at
  `golem.WithOutputTool`, before any network call.

## v0.3.0 — 2026-08-18

This minor release makes conversations richer and longer-lived: images in
prompts, bounded history, and models that survive each other's failures.
Every addition is opt-in and additive; existing call sites and persisted
message JSON are unchanged.

### Added

- **Multimodal input.** `model.Message` carries image parts — a URL the
  provider fetches or inline bytes with a media type — appended after the
  prompt text through `WithPromptImageURL` and `WithPromptImageData` run
  options, on `Run` and its history and streaming variants. Parts are
  validated before any model call and ride the additive-only message JSON,
  so persisted history decodes unchanged (ADR 0011). All five adapters
  translate them: openai, azure, and anthropic accept URLs and inline
  data; gemini maps URLs to file data that must be provider-reachable;
  bedrock accepts inline bytes only and fails URL parts with a typed
  error instead of dropping content.
- **History processing.** `WithHistoryProcessor` installs a function that
  rewrites a run's supplied history once, before validation and repair.
  The `TrimHistory` builtin keeps the newest messages and cuts at turns
  that can open a request, so long conversations stay bounded without
  synthesized repair evidence.
- **Fallback model.** `model.NewFallback(primary, alternates...)` tries
  its models in order on retryable-classified failures; the first success
  answers, non-retryable failures return immediately, and the last error
  keeps its classification for the run's retry policy. Streams fall back
  only before the first forwarded fragment.
- **Activity-based usage bounds.** `UsageLimit` gains `Requests` and
  `ToolCalls` dimensions alongside its token bounds. The run counts
  provider calls — retried attempts included — and tool executions, and
  fails at the usage stage with a `UsageLimitError` naming the crossed
  dimension.
- **Documentation website.** The guides publish as a MkDocs Material
  site with a strict build in CI; deployment stays gated until the
  repository goes public (see `docs/website.md`).

### Fixed

- The guides' reading order regains the Tool timeouts entry the site
  navigation already listed.

## v0.2.0 — 2026-08-16

This minor release expands the agent and tool contracts with new, opt-in
capabilities while retaining the existing default execution behavior.

### Added

- `testmodel`: exported scripted, function, and streaming model fakes for
  deterministic, provider-free agent tests. Recorded requests are deep-copied
  so assertions cannot mutate the captured evidence.
- Tool-mode structured output via `WithOutputTool`, for models that support
  tool calling but not native JSON-schema response formats.
- Fine-grained tool controls: per-tool retry limits and deadlines,
  `WithToolChoice` to constrain advertised capabilities, and opt-in ordered
  parallel execution. A `tool.Tool.Sequential` declaration forms a barrier
  for side-effecting work.

### Changed

- Tool correction budgets are now tracked independently for each tool; the
  agent-level `WithToolRetries` setting remains the default and can be
  overridden per tool.
- Tool results from a parallel batch are retained in the model's original call
  order, independent of completion order.

### Fixed

- Resuming a conversation repairs incomplete tool-call/result pairs before a
  provider receives the history, preventing invalid dangling calls.

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
