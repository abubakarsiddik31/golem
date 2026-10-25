# Golem roadmap

Direction, not promises: items move or drop as users report what they
need. Every shipped feature arrives with its guide, a runnable example,
and contract tests, per the contributor rules in
[AGENTS.md](../AGENTS.md).

## Where we are

v1.0.0 is released: the compatibility promise is ADR 0041 — SemVer,
additive-only below v2 — and
every pre-freeze and parity item has landed. In-tool run cancellation
cleanly stops a
run with evidence on `RunError.Partial`; `golem.SanitizeHistory`
sanitizes histories at
untrusted boundaries; `package pdfextract` and `package docextract`
provide layout-aware document extraction across PDF, Word, Excel,
PowerPoint, Markdown, and CSV, with scanned PDFs transcribed offline by
the local Indra OCR CLI under explicit per-page routing
(`auto`/`prefer-text`/`force-ocr`) and provenance (`Page.Source`); a
`tokens.Counter` port prices a request
before it is sent and bounds history by token budget; user-supplied
`model.Price` rates report and bound a run's dollar cost; tools return
image and document parts as evidence and can fail definitively without
consuming retry budget; `golem.NormalizeHistory` repairs and reports
damaged pairings while keeping truncated arguments verbatim; every run
stamps a fresh `RunID` on its events, result, and messages, and
conversations carry a `ConversationID` through history and storage;
`package mcp` bridges a server's tools and exposes its resources and
prompts as application-driven client operations; deferred tool loading
keeps a large catalog behind a runner-owned `search_tools` discovery
tool, with reveals derived from history; and
the durable message-JSON contract is under continuous fuzz, with `-race`
CI and the offline examples running on every check. The parity series
added benchmarks with a recorded baseline, run boundary events and the
`telemetry` tracing port, the `session` persistence port, the
retrieval index and its agent tool, DeepSeek reasoning capture and the
Groq preset, and the orchestration and templating recipes.

## The series that built v1.0.0

A parity pass against Pydantic AI and a survey of the Go ecosystem
(cloudwego/eino) produced a pre-freeze series. Each item lands as its
own PR with its guide, example, and contract tests; the hardening item
rides across the series. Items move or drop on evidence, and the
series ends in the freeze, not a date.

- ~~**Token counting and budget-aware history bounding.**~~ Shipped in
  v0.7.6 (ADR 0023).

- ~~**Tool-result parts.**~~ Shipped in v0.7.6 (ADR 0025), including
  the definitive-failure semantics (`&tool.Failed{}`) it absorbed.

- ~~**Result cost.**~~ Shipped in v0.7.6 (ADR 0024).

- ~~**History repair.**~~ Shipped in v0.7.6 as `golem.NormalizeHistory`
  with the per-adapter `truncated_args` wire encoding (ADR 0026).

- ~~**Run and conversation IDs.**~~ Shipped in v0.7.6 (ADR 0027).

- ~~**In-tool run cancellation.**~~ Shipped as `&tool.Canceled{}`: a
  tool ends the run deliberately at the `canceled` stage, evidence
  preserved and resume-ready on `RunError.Partial` (ADR 0028).

- ~~**Untrusted-history sanitization.**~~ Shipped as
  `golem.SanitizeHistory`: drops system messages and non-http(s) URL
  parts, repairs pairing, and reports every change at the trust
  boundary (ADR 0029).

- ~~**MCP breadth: resources and prompts.**~~ Shipped as typed client
  operations — `ListResources`, `ListResourceTemplates`, `ReadResource`,
  `ListPrompts`, `GetPrompt` — rather than bridged tools, because the
  protocol makes both application-driven (ADR 0032).

- ~~**MCP client breadth: metadata, progress, elicitation, and
  authorization.**~~ Shipped as tool annotations and structured results,
  `OnProgress` for in-flight calls, `OnElicitation` over multi
  round-trip requests, and `TokenSource` plus `AuthError` challenge
  reporting for the HTTP transport. Sampling and roots are deprecated by
  the `2026-07-28` revision and stay unimplemented; OAuth itself stays in
  the application (ADRs 0034, 0035, 0036, 0037).

- ~~**Tool search / deferred tool loading.**~~ Shipped as
  `WithDeferredTools` plus a runner-owned `search_tools` discovery tool:
  deferred tools stay declared and dispatchable but out of the prompt
  until matched, and reveals are derived from history rather than
  stored, so they survive correction rounds and resumed runs (ADR 0033).
  Provider-native search stays post-v1.

- ~~**Test hardening.**~~ Shipped in v0.7.6: race-detector CI,
  continuous fuzzing of the message-JSON contract, the opt-in live
  smoke matrix, and CI running the offline examples.

- ~~**Benchmarks.**~~ Shipped as `-benchmem` suites over the runner
  loop and the two document extractors, `scripts/bench.sh` for
  before/after `benchstat` comparison, and a recorded baseline in
  `docs/benchmarks.md` that explains what each number means.
  Deliberately outside the CI gate: absolute numbers vary by machine,
  so the gate is a diff on a change that touches one of those paths.

- ~~**Observability: run boundaries and a tracing port.**~~ Shipped as
  `run_start`/`run_end` boundary events bracketing every pass of the
  loop, and `package telemetry` — a `Tracer`/`Span` port in
  OpenTelemetry's shape plus an observer that turns the event stream
  into a span tree, so an application's OTel SDK plugs in behind a
  dozen-line adapter with no dependency on Golem's side (ADR 0038).

- ~~**Sessions.**~~ Shipped as `golem.WithSession(store)` plus the
  `session.Store` port: a run loads its stored conversation before it
  runs — through the same history processor as hand-passed history —
  and saves it after, partial evidence included so failures and pauses
  resume from the store. `session.Memory` is the offline
  implementation; file and database backends stay application-side
  behind two methods (ADR 0039).

- ~~**Orchestration.**~~ Shipped as the composition guide and
  `examples/orchestration`: fan-out/fan-in, pipelines, routers, and
  bounded maps written in plain Go over the existing seams — no new
  API. The claim is deliberate: shared agents already serve concurrent
  runs and results are already immutable, so a graph engine would
  re-express the language; it stays out per the foundation non-goals.

- ~~**Retrieval.**~~ Shipped as `package retrieval`: the `Index` port
  (add embedded documents by ID, search a query vector for top-k by
  cosine), `retrieval.Memory` as the offline in-process
  implementation, and the `search_documents` agent tool wired from any
  `embedding.Embedder`. Chunking and vector-database backends stay
  application-side behind the two methods (ADR 0040).

- ~~**Provider breadth: DeepSeek reasoning and Groq.**~~ Shipped in
  `providers/openai`: leading `<think>` blocks join
  `reasoning_content` as captured thinking, in streams and plain
  responses, so DeepSeek R1 works through any completion runtime.
  `providers/groq` presets the base URL and key handling for Groq
  without duplicating the wire protocol (closes #54, #55).

- ~~**Prompt templating.**~~ Shipped as the recipe in
  [Getting started](guides/getting-started.md#prompt-templating) and
  `examples/prompt-template`: `WithInstructionsFunc` plus Go's
  `text/template` over typed dependencies — parse once, execute per
  run. No first-party template engine; the stdlib one is the point.

- ~~**Prompt caching.**~~ Shipped in v0.9.0: `anthropic.CacheControl`
  and `bedrock.Config.CacheControl` place explicit checkpoints,
  OpenAI, Azure, and Gemini cache eligible prefixes implicitly, and
  the cached share arrives on `Usage.CacheReadTokens`
  (see [Providers](guides/providers.md#prompt-caching)).

The series is closed and the freeze is written: ADR 0041 records the
compatibility promise — SemVer, additive-only below v2, deprecations
documented and removed only at v2, `apidiff` between the last v1 tag
and every release — and the v1 criteria are met: documentation
complete (31 guides), no known provider gaps, MCP shipped, and the
public surface reviewed through the parity series itself. The next
release is v1.0.0.

## After v1 — additive minors

- **Stream retry beyond the first fragment**, only if usage demands a
  replay-safe design; today streamed turns are single-attempt by
  design.
- **Toolset grouping**, only if MCP usage demands a grouping concept.
- **Per-step tool preparation** (filter or rewrite the tool list per
  request) and **native provider-executed tools** (server-side search,
  code execution), demand-gated.
- **MCP server mode**, exposing a Golem agent as an MCP server.
- **Agent2Agent (A2A) protocol**, an agent-serving surface like MCP
  server mode and deferred for the same reason: no requester yet, and
  the seams an adapter would use — agent-as-tool outward, the model
  port inward — are the ones v1 froze. When users ask, it lands
  additively.

## Not planned

An evals harness, a graph or workflow engine, durable execution, and
CLI, UI, or gateway products stay out per the
[foundation brief](foundation.md) non-goals; revisit when users ask.
Agent-loop middleware frameworks and YAML agent specs are rejected for
the small core: the history-processor and run-event seams remain the
extension points.
