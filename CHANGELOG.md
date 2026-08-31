# Changelog

## v0.7.2 — 2026-08-31

This patch adds agent skills — the standard `SKILL.md` folders loaded
on demand — and makes run events routable per request. Skills follow
the open Agent Skills format: the new `skills` common tool discovers
them from application-configured directories, catalogs each skill's
name and description in the tool description, and returns the full
instructions when the model asks for one. A shared agent can also
route each request's events separately through a run-scoped observer.

### Added

- **Agent skills.** `skills.New[Deps]` scans application-configured
  directories for `<name>/SKILL.md` folders (the agentskills.io open
  standard — the same layout this repository uses for its own
  development skills), validates each against the format limits, and
  returns an ordinary tool named `skill`. Only each skill's name and
  description enter the model's context, as an `<available_skills>`
  catalog appended to the tool description; a matching task loads the
  full instructions plus the skill's base directory and supporting
  files. Discovery is strict and immutable per tool value, and skill
  directories are always application-resolved — never the working
  directory, home, or environment. See the
  [agent skills guide](docs/guides/skills.md) and ADR 0019.
- **Run-scoped run events.** `WithRunObserver` registers a run option
  that delivers the same events `WithRunEvents` does — provider call
  attempts, tool executions, correction boundaries — for a single run,
  so a shared agent (a server handling many requests) routes each
  request's events without rebuilding the agent. The run's observer
  composes with the agent's: construction-scoped first, then the
  run's, per event. Accepted by `Run` and its history, streaming, and
  deferred-resume variants. See the
  [run events guide](docs/guides/run-events.md).

## v0.7.1 — 2026-08-30

This patch makes failures preserve their evidence, adds run-scoped
event observers, and closes a Gemini streaming gap. Runs that fail
mid-flight — cancellation and client disconnects included — keep their
partial transcript, usage, and activity counts on `RunError.Partial`;
a shared agent can route each request's events separately; and a
Gemini stream truncated in transit fails instead of billing as a short
answer.

### Added

- **Run-scoped run events.** `WithRunObserver` registers a run option
  that delivers the same events `WithRunEvents` does — provider call
  attempts, tool executions, correction boundaries — for a single run,
  so a shared agent (a server handling many requests) routes each
  request's events without rebuilding the agent. The run's observer
  composes with the agent's: construction-scoped first, then the
  run's, per event. Accepted by `Run` and its history, streaming, and
  deferred-resume variants. See the
  [run events guide](docs/guides/run-events.md).
- **Partial run evidence.** A run that fails after it began producing
  evidence now carries it on `RunError.Partial`: the conversation
  through the last completed model turn, the usage those turns
  reported, and the counts of model requests (failed attempts
  included) and tool executions attempted. `Partial` is nil when the
  run failed before completing a model turn, reporting usage, or
  executing a tool. `Partial.Messages` is resume-ready history for
  `RunWithHistory` — repair synthesizes results for calls a mid-batch
  failure left unanswered. See the
  [conversations guide](docs/guides/conversations-and-history.md) and
  the offline `partial-evidence` example.

### Fixed

- **Gemini stream truncation.** A Gemini SSE stream that ended without
  a chunk carrying a terminal `finishReason` — a network truncation —
  assembled the partial text and returned it as a complete answer. It
  now fails with the adapter's `DecodeError`, matching the
  `[DONE]`/message-stop sentinel checks of the OpenAI-compatible,
  Azure, Anthropic, and Bedrock adapters, so a truncated stream no
  longer bills as a short success.

### Changed

- **Cancellation and deadline errors ride `RunError`.** They were
  previously returned unwrapped, which discarded their partial
  evidence; they now wrap like every other failure and stay matchable
  with `errors.Is(err, context.Canceled)` through `RunError.Unwrap`.
  A tool timeout or a run cancelled inside a tool now reports the
  `tool` stage instead of no stage at all.

## v0.7.0 — 2026-08-30

This minor release adds three capabilities: reasoning as first-class
evidence across every adapter, runs that can pause for human approval
or external results and resume in-process or across processes, and
first-class coverage for local runtimes — Ollama and LM Studio. Every
addition is opt-in and additive; existing call sites and persisted
message JSON are unchanged.

### Added

- **Thinking content.** Reasoning is now represented end to end.
  Assistant messages carry ordered `model.ThinkingBlock`s (visible
  text with the provider's verification `Signature`, or the encrypted
  `Redacted` payload), `model.ToolCall` carries reasoning evidence
  bound to a call, and stream deltas carry `model.ThinkingDelta`
  fragments. The agent validates thinking placement on history before
  any model call, `testmodel.Scripted` replays and clones it, and the
  runner's evidence keeps blocks and signatures intact so multi-turn
  conversations verify. Enablement stays per adapter:
  `anthropic`/`bedrock` gain a `ThinkingConfig` (adaptive, budget, or
  disabled) plus `Effort`, `gemini` gains a `ThinkingConfig`
  (include-thoughts, budget, or level) with `thoughtSignature`
  round-trip, and `openai`/`azure` gain `ReasoningEffort` with
  capture-only handling of the non-standard `reasoning_content` field.
  Existing call sites and persisted message JSON are unchanged. See
  the [thinking guide](docs/guides/thinking.md) and ADR 0017.
- **Deferred tools.** A tool can now pause the run instead of
  producing a result: returning `*tool.Deferred` (approval or
  external-result kind) ends the run cleanly with the call pending on
  `Result.Pending`, while co-emitted calls still execute. The
  application resumes with `RunWithDeferredResults` — approved calls
  re-execute the tool with `tool.CallApproved` set so gated actions
  happen only after sign-off, denials and external results become the
  calls' tool results in emission order, and validation fails before
  any model call. A `deferred` run event marks the pause point.
  Providers need nothing: between pause and resume no model call
  happens. See the [deferred tools
  guide](docs/guides/deferred-tools.md) and ADR 0018.
- **Local models documented: Ollama and LM Studio.** Local
  OpenAI-compatible runtimes now have first-class coverage: the
  providers guide lists LM Studio's endpoint, explains the
  placeholder-API-key convention, and summarizes what holds over each
  runtime — streaming with `include_usage`, tool calling (including
  the single-chunk complete-call shape these servers emit, now pinned
  by an adapter test), `json_schema` structured output, image parts,
  and `ReasoningEffort` — plus the model-dependent caveats. The new
  `examples/local-models` runs the same agent against either runtime,
  printing setup instructions when `GOLEM_LOCAL_BASE_URL` is unset.

## v0.6.0 — 2026-08-22

This minor release lands the three post-v0.3 milestones together:
composition and control (agent delegation, run events, request
tuning, and the web fetch, file read, and command tools), ecosystem
interop (the MCP client with stdio and streamable HTTP transports),
and the last provider gap (Bedrock streaming). Every addition is
opt-in and additive; existing call sites and persisted message JSON
are unchanged.

### Added

- **Bedrock streaming.** The Bedrock adapter implements
  `model.StreamingModel` over the ConverseStream endpoint: the AWS
  binary event-stream framing (prelude, typed headers, CRC32 checks) is
  decoded with the standard library — no AWS SDK — forwarding text and
  tool-use fragments as deltas and assembling the same normalized
  `Response` as `Generate`. Mid-stream exception frames
  (`throttlingException`, `modelStreamErrorException`, …) classify
  through the existing `APIError` retryability rules, and a stream that
  ends without a `messageStop` event fails as a decode error rather
  than yielding a silent partial response.
- **MCP streamable HTTP transport.** `mcp.NewHTTP` reaches remote MCP
  endpoints behind the same transport interface as stdio: one POST per
  message, answered as a JSON body or a text/event-stream read event
  by event; the server-assigned session header is captured and sent
  automatically, extra headers (such as `Authorization`) ride on every
  request, and non-2xx statuses surface as the typed
  `HTTPStatusError`.
- **MCP client.** The new `mcp` package connects agents to Model
  Context Protocol servers: `NewStdio` runs a server subprocess over
  stdio, `Client.Initialize` performs the handshake, and
  `AsTools[Deps]` returns every server tool as an ordinary
  `tool.Tool` — name, description, and schema carried verbatim from
  the server. Executing a bridged tool calls `tools/call` with the
  model's raw arguments; `isError` results reject as correctable
  `ModelRetry` while protocol failures surface as the typed
  `ProtocolError` at the tool stage. The client is synchronous — one
  in-flight request, no goroutines — with context cancellation
  throughout, answering server pings inline (ADR 0016).
- **Command execution tool.** The `shell` package builds a `run_command`
  tool that executes one command through the platform shell and returns
  combined stdout and stderr capped by a byte budget. A non-zero exit
  status is evidence for the model — the result ends with an exit
  status line rather than failing — while the timeout (always on,
  60s by default) and cancellation fail at the tool stage with the
  context error preserved. Strictly opt-in: the command runs with the
  registering process's privileges (ADR 0015).
- **File read tool.** The `fileread` package builds a `read_file` tool
  confined to a configured root directory: relative paths only, `..`
  segments and symlinks escaping the root reject as correctable
  `ModelRetry`, missing paths and directories reject as correctable,
  bodies are capped by a byte budget with a truncation marker, and
  sniffed binary content fails as a typed error at the tool stage
  (ADR 0015).
- **Web fetch tool.** The new `webfetch` package is Golem's first common
  tool: `webfetch.New[Deps]`/`MustNew[Deps]` build an ordinary
  `tool.Tool` — name `web_fetch`, single `url` argument — that GETs an
  http(s) URL and returns the body as model-readable text: HTML and
  XHTML stripped to visible text with entities unescaped, other
  text-like types passed through, bodies capped by a byte budget with a
  truncation marker, and an injectable HTTP client and per-fetch
  timeout keeping tests offline. Malformed or non-http url arguments
  reject as correctable `ModelRetry`; non-2xx statuses and unsupported
  media types surface as typed errors at the tool stage (ADR 0015).
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
