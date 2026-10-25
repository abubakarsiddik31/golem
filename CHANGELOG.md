# Changelog

## Unreleased

### Added

- **The v1 freeze (ADR 0041).** The compatibility promise that makes the next release mean something: SemVer with an additive-only exported surface below v2, checked by `apidiff` between the last v1 tag and every release; the durable contracts (`model.Message` JSON, run event kinds, telemetry attribute keys, session semantics) stay durable; deprecations are documentation, not removals; behavior fixes are not breaking. The post-v1 deferral list is explicit — stream retry, toolset grouping, per-step tool preparation, native provider-executed tools, MCP server mode, and the Agent2Agent protocol, the last two deferred for the same reason: they are agent-serving surfaces with no requester yet, and the seams they would use are the ones v1 froze. Non-goals survive unchanged.

- **DeepSeek reasoning capture and a Groq preset.** `providers/openai` now extracts a leading `<think>` block from message content into thinking — how DeepSeek R1 reports reasoning through generic completion runtimes — beside the existing `reasoning_content` capture, in both streamed and plain responses, with tags split across stream fragments handled and a `<think>` mentioned mid-answer left as content. `providers/groq` is a preset, not a protocol: it fills Groq's base URL, requires the API key, and names catalog constants; everything else is the OpenAI adapter, because Groq speaks the same wire. New offline examples `examples/deepseek` and `examples/groq` (closes #54, #55).

- **Prompt templating through the standard library.** The getting-started guide gains a templating recipe — `WithInstructionsFunc` rendering a `text/template` parsed once at construction over the run's typed dependencies — and `examples/prompt-template` runs it offline, showing two agents render the same template differently from their dependency values. Golem ships no template engine on purpose: instructions are already a function of the run context, and the stdlib is the templating layer.

- **Retrieval: an index port, an in-memory cosine index, and a `search_documents` tool.** `package retrieval` is the search half of RAG, beside the embeddings port that produces vectors. `retrieval.Index` is two methods — `Add` embedded documents by ID (replace semantics, so re-indexing is idempotent) and `Search` a query vector for top-k by cosine similarity, best first with deterministic ID tie-breaking; `retrieval.Memory` implements it in-process with float64 accumulation, a defined zero rule instead of NaN, and typed `*DimensionError` failures on width mismatches. `retrieval.MustNew` builds the `search_documents` agent tool: it embeds the model's query through the same `Embedder` that embedded the corpus, renders hits as ranked text with IDs, scores, and metadata, rejects malformed arguments as correctable `*model.ModelRetry`, and lets embedding-provider failures propagate plainly — a quota problem is not a model mistake. Chunking and backend choice stay with the application; a vector database implements the same two methods (ADR 0040).

- **Sessions: conversations that persist between runs.** `golem.WithSession(store)` loads the stored history under the run's conversation ID before the run — flowing it through the same history processor and validation as hand-passed history — and saves the full conversation after it. The save keeps failures resumable: `RunError.Partial.Messages` after a failed run, the paused transcript of a run waiting on approvals, and nothing when a failure produced no evidence, so an activityless crash never clobbers a stored conversation. `package session` owns the port: a two-method `Store` (Load/Save by conversation ID, value semantics, snapshot required) with `session.Memory` as the offline in-process implementation that snapshots through the durable message JSON itself. A load failure fails the run before any model call; a save failure returns the run's result and the save error together, because the model did answer and only the persistence failed. File and database backends stay application-side behind the same interface (ADR 0039).

- **Orchestration guide: multi-agent workflows in plain Go.** The parity answer to graph-engine frameworks is a recipe book, not an API: fan-out/fan-in over concurrent specialists, sequential pipelines with typed hand-offs, pre-run routing versus model-driven delegation, and bounded batch maps — each a short snippet over the existing seams (`Run`, structured output, sessions, `AsTool`), with the ownership rules stated: the application owns the goroutines, cancellation, and error delivery; Golem guarantees shared agents serve concurrent runs and results stay immutable. `examples/orchestration` runs the fan-out-and-pipeline pattern offline. No new API; a graph/workflow engine stays out per the foundation non-goals.

- **Spans through the `telemetry` port, and run boundary events.** Every run now emits `run_start` and `run_end` around its pass through the execution loop — the first and last events on every terminal path, with `run_end` carrying the pass's usage and terminal error — so an observer can root a span tree. `package telemetry` builds that tree for you: `telemetry.Observer(tracer)` adapts a `Tracer` — an interface in OpenTelemetry's shape that the application implements against its SDK — into a `golem` observer, opening one `agent run` root per pass, a client `model request` span per provider attempt, and a `tool <name>` span per execution, attributed with run and conversation IDs, turn and attempt, usage, tool arguments and results, and decoder rejections. A correction round is a second bracketed pass under the same `golem.run.id`, its root naming how many rejections preceded it. The OpenTelemetry SDK stays out of the module's dependency graph; the guide shows the full adapter. Tool arguments and results are recorded verbatim — redact in the adapter if your tools handle secrets (ADR 0038).

- **`mcp` supports per-request credentials and reports authorization challenges.** `HTTPConfig.TokenSource func(context.Context) (string, error)` is called before every POST, so a refreshed token takes effect without rebuilding the transport; a non-empty result is sent as `Authorization: Bearer …` and replaces any `Authorization` from `Header`, an empty result sends none, and an error fails the request before it is sent. A 401 or 403 now returns `*mcp.AuthError`, which carries the raw `WWW-Authenticate` value and the parsed `Challenge{Scheme, Realm, Error, Description, Scope, ResourceMetadata}` — enough to distinguish an expired token from a missing scope and to find the RFC 9728 protected-resource metadata. `AuthError` unwraps to `*HTTPStatusError`, and other non-2xx statuses are unchanged. Golem implements no OAuth flow and never retries; discovery, registration, PKCE, refresh, and step-up stay in the application (ADR 0037).

- **`mcp` answers elicitation through multi round-trip requests.** `(*Client).OnElicitation(handler)` receives the `elicitation/create` requests a modern server sends inside an `input_required` result, as `Elicitation{Message, Mode, RequestedSchema, URL}`, and answers with `ElicitationResult{Action, Content}` — `accept`, `decline`, or `cancel`. With a handler installed, every modern request declares the `elicitation` capability; without one it declares nothing. The client retries the original call with `inputResponses` and the server's `requestState` echoed verbatim, each retry carrying a fresh JSON-RPC id, and is cut off after eight rounds. The handler runs inline under the client lock, form content is not validated against the schema, and url mode is never fetched or opened — the handler must show the full URL and obtain consent. Sampling and roots are deprecated by the `2026-07-28` revision and are not implemented; those request kinds, an elicitation with no handler, and a result with nothing to answer all fail with `*mcp.InputRequiredError`, which now names the unsupported `Method` (ADR 0036).

- **`mcp` reports progress for in-flight calls.** `(*Client).OnProgress(handler)` receives the server's `notifications/progress` updates as `Progress{Completed, Total, Message}`. With a handler installed, every request carries a fresh `progressToken` in its `_meta` — in both protocol eras — and a notification whose token does not match the request in flight is dropped, so a late update from an earlier call is never misreported. The handler runs inline on the calling goroutine while the client holds its lock, so it must be quick and must not call back into the client; passing nil stops requesting progress (ADR 0035).

- **`mcp` surfaces tool metadata and structured results.** `ToolInfo` now carries the server's `Title`, `OutputSchema`, and `Annotations` — the protocol's read-only, destructive, idempotent, and open-world hints, each a `*bool` so "not declared" stays distinct from an explicit false. Golem never enforces a hint; an approval flow can read one to decide what to ask before a call. `CallResult` carries `StructuredContent`, and `CallResult.Text()` falls back to it when a tool answers with structured data and no content, so the model sees the payload instead of an empty result. The payload is not validated against the declared schema — it is server-produced and untrusted (ADR 0034). `AsTools` is unchanged.

- **Tool search keeps a large catalog out of every request.** `golem.WithDeferredTools[Deps, Output](tools ...tool.Tool[Deps])` declares tools that stay dispatchable but unadvertised until the model finds them, and `golem.WithToolSearchLimit[Deps, Output](limit int)` caps matches per search (default 5). When anything is deferred, the runner synthesizes one `search_tools` tool taking `{"query": string}` and advertises it beside the eager tools. Matching is deterministic and stdlib-only — query tokens score against tool names (3), schema property names (2), and descriptions (1), with prefix hits one lower, ranked by score then name — and the result text lists the matches with their arguments. Reveals are derived from the conversation history on every turn, so they survive output-correction rounds and resumed runs with no stored state; a call to an unrevealed deferred tool fails at the tool stage naming `search_tools`. `search_tools` is a reserved name, `WithToolChoice` and `WithDeferredTools` are mutually exclusive, and bad search arguments are a correctable `*model.ModelRetry`. Provider-native tool search remains post-v1 (ADR 0033).

### Changed

- **Benchmark suites and a recorded baseline.** `internal/runner`, `pdfextract`, and `docextract` now carry `-benchmem` benchmarks over the paths users pay for: the runner loop (single turn, tool round, 201-message history, history repair, history encoding) and document extraction (one page, twenty pages, parser-only, docx/xlsx/pptx, outline-only). `scripts/bench.sh` runs all three with three repetitions for `benchstat` comparison; `docs/benchmarks.md` records the baseline and what each number means. Benchmarks are not part of the CI gate, because absolute numbers vary by machine and a threshold would be flaky — the gate is a before/after `benchstat` diff on a change that touches one of these paths. They do not run under `go test ./...`, so coverage is unaffected.

## v0.9.0 — 2026-10-25

This minor release completes the MCP client — resources and prompts on
top of tools, plus both protocol eras — and closes the gaps found in the
pre-v1 readiness review: two hardening fixes (`webfetch` SSRF,
`docextract` decompression), a hardened PDF parser, and the
document-extraction contract corrections that followed. The API is
additive — no exported symbol changed shape — but a few documented
behaviors were corrected; each is called out under Fixed.

It also publishes `v0.8.6`, which was documented below but never tagged;
that release is now tagged at its original commit, so the history below
is accurate.

### Added

- **`mcp` reads resources and renders prompts.** The client is no longer tools-only: `(*Client).ListResources`, `ListResourceTemplates`, and `ReadResource(ctx, uri)` cover the resource side, and `ListPrompts` plus `GetPrompt(ctx, name, arguments)` cover prompt templates. They are typed client operations rather than bridged tools, because MCP makes both application-driven — the host decides what to load and what to render, so a model-chosen URI or prompt would be authority it should not have (ADR 0032). Every list follows pagination cursors. `ResourceContent` carries `Text` or a decoded `Blob`; `PromptResult` carries `PromptMessage` values whose `Content` is a typed block, with `PromptMessage.Text()` rendering the text-bearing ones. An empty URI or prompt name fails locally before any request is sent. Sampling, elicitation, roots, subscriptions, and `x-mcp-header` remain unimplemented.
- **`mcp` speaks both MCP protocol eras.** `Initialize` now probes `server/discover` for the stateless `2026-07-28` revision and falls back to the `initialize` handshake for legacy servers, so the same client works with today's servers and the newer stateless ones. Modern requests carry the required per-request `_meta` (`protocolVersion`, `clientInfo`, `clientCapabilities`); over streamable HTTP they also carry the `MCP-Protocol-Version`, `Mcp-Method`, and `Mcp-Name` headers, with unsafe names Base64 encoded, and never a session header. `(*Client).ProtocolVersion()` reports the negotiated revision. A modern server that does not support `2026-07-28` fails `Initialize` with `*mcp.UnsupportedProtocolVersionError`; a modern `input_required` result fails with `*mcp.InputRequiredError` because sampling, elicitation, and roots are not implemented. Legacy behavior — the handshake, session header, and inline `ping` replies — is unchanged.

### Security

- **`webfetch` refuses non-public destinations by default.** A model-produced URL can no longer reach loopback, private, link-local, multicast, unspecified, or carrier-grade NAT addresses, closing the SSRF path to internal services and cloud metadata endpoints. Hostnames are resolved and every address must be public; the dialer validates the address it actually connects to, and every redirect hop is re-checked. The refusal is a `*webfetch.BlockedAddressError` at the tool stage, not a correctable model mistake. `webfetch.Config.AllowPrivateNetworks` opts out for agents meant to fetch from a trusted private network.
- **`docextract` bounds Office archive decompression.** Word, Excel, and PowerPoint archives are now rejected when a single entry exceeds `DefaultMaxEntryBytes` (64 MiB) or the archive total exceeds `DefaultMaxUncompressedBytes` (256 MiB). Declared central-directory sizes are checked before parsing and every actual read is capped, so a forged header cannot stream a zip bomb through the XML decoder. The refusal is a `*docextract.DecompressionLimitError`.
- **`pdfextract` bounds parser work and refuses encrypted input.** `ParsePDF` and `ExtractPage` recover at their boundaries, so malformed input returns an error instead of crashing the process. Page-tree recursion is capped at 64, object nesting at 256, and a single decompressed stream at 64 MiB; xref `/W` widths and `/Columns` are validated. A PDF with an `/Encrypt` dictionary fails with `pdfextract.ErrEncrypted` rather than emitting ciphertext as text.

### Fixed

- **`pdfextract` extraction contracts are consistent.** `Page.IsScanned` is now pure detection and no longer changes with `DisableOCR`, which is applied after mode routing and so wins over `ModeForceOCR`. A page that fails to extract stays in `Document.Pages` with `Page.Err` set and a Markdown marker, so page counts stay accurate. `Options.MaxBytes` is honored by `ExtractBytes` and truncates on a UTF-8 rune boundary. `ReturnImageParts` and `ReturnScannedPageParts` are independent and de-duplicate shared images, and `UnsupportedFileError` unwraps to the parse error.
- **`pdfextract` OCR never fabricates text.** A nil `OCREngine` means no OCR, and a scanned page stays `PageSourceEmpty`. `NoopOCR`, `DefaultOCREngine`, and the deprecated `AppleVisionOCR` report `ErrNoOCREngine` instead of returning placeholder text, and `CommandOCR` writes the page image to a temp file and passes its path.
- **`pdfextract` decodes more PDF content correctly.** `/Filter` arrays, ASCII85, and ASCIIHex are decoded, and unknown filters are rejected instead of silently returning encoded bytes. Form XObjects are interpreted recursively to a depth of 8. Images are converted by `/ColorSpace` and `/BitsPerComponent` — 8-bit Gray, RGB, CMYK, and 1-bit — with CMYK ink amounts inverted so a no-ink pixel is white rather than black. Fraction reconstruction and multi-column table heuristics were corrected alongside.
- **`pdfextract` reports image write failures and pads tables by display width.** A failed `ImageDir` write is recorded in `ImageRef.Err` while the bytes stay in memory and `ImageRef.Path` keeps the relative placeholder path, instead of advertising a path nothing was written to. Markdown tables now measure display width rather than byte length, so CJK and emoji cells align.

## v0.8.6 — 2026-10-18

This release adds first-class, offline OCR for scanned PDFs to `package pdfextract`: a local subprocess engine, explicit per-page routing between the text layer and OCR, per-page provenance, and structured tables recovered from OCR. All changes are additive and zero-value compatible; existing call sites are unchanged.

### Added

- **Local offline OCR engine (`IndraOCREngine`).** Transcribes scanned pages on the host CPU by shelling out to the external `indra-ocr` CLI ([datalab-to/indra](https://github.com/datalab-to/indra)) — offline, no API key, no Go dependency, and no model shipped with Golem. Standard library `os/exec` only, with no cgo. `IndraOCRConfig.Run` is an injectable subprocess seam, so contract tests run with no binary, model, or network, and every call is bounded by a context deadline that kills the process on cancellation. Construction fails fast when the binary is absent from `PATH` (ADR 0030).
- **Explicit OCR routing (`OCRMode`).** `Config.Mode`/`Options.Mode` decides per page between `ModeAuto` (OCR only pages whose text layer is missing or unusable — the default), `ModePreferText` (never OCR), and `ModeForceOCR` (OCR every page image, ignoring a broken text layer). `DisableOCR` still wins over every mode, and an unknown mode fails construction. Detection is decoupled from routing: `Page.IsScanned` always reports what the page looks like, while `Mode` decides what is done with it (ADR 0031).
- **Per-page provenance (`Page.Source`).** Every page records how its text was produced — `digital`, `ocr`, `hybrid`, or `empty` — so callers can tell a deterministic text-layer parse apart from OCR output and key trust, caching, and QA decisions on it. `Page.Source` reports the outcome, not the intent: a scan the caller chose not to OCR stays `IsScanned: true, Source: "empty"` (ADR 0031).
- **Structured OCR tables (`MarkdownResult.Tables`).** An OCR engine can now return structured tables alongside its Markdown; `IndraOCREngine` recovers the HTML tables Indra emits and returns them as `Table` values. `Document.Tables` is uniform across digital and scanned pages, so callers read cells the same way for both. `Table.BBox` and `Table.Ruled` stay unset for OCR tables, which report structure rather than vector geometry (ADR 0031).

## v0.8.5 — 2026-10-06

This patch release makes `APIKey` optional in `package openai` (`New` and `NewEmbedder`) when configuring custom or local endpoints (such as Ollama, LM Studio, or vLLM), omitting the Authorization header instead of rejecting the call or requiring dummy keys.

### Fixed

- **Optional `APIKey` for custom `BaseURL` in `package openai`.** When `BaseURL` is set to any custom endpoint, callers no longer need to pass a placeholder or dummy `APIKey`. When `APIKey` is empty, Golem omits the `Authorization` header entirely. The default OpenAI endpoint (`https://api.openai.com/v1`) continues to require a non-empty `APIKey`.
- **Local models example and guides simplified.** Cleaned up `examples/local-models/main.go`, `docs/guides/providers.md`, and `docs/guides/pdf-extract.md` to omit placeholder keys when using local runtimes like Ollama and LM Studio.

## v0.8.4 — 2026-09-21

This patch release adds generalized scanned PDF page analysis and image handling to `package pdfextract`, supporting agent multimodal parts, small vision models (Gemini Flash-Lite, local Ollama), and Mistral OCR with caller-supplied cost and usage tracking.

### Added

- **Scanned page analysis with multimodal parts.** `extract_pdf` supports `ReturnScannedPageParts: true` to attach full-page scanned raster images as `model.Part` in `tool.Result.Parts` (per ADR 0025) for direct visual inspection by multimodal models (Claude, GPT-4o, Gemini).
- **Small vision model transcription (`ModelOCREngine`).** Transcribes scanned pages into structured Markdown (with headings and tables) using any `model.Model`, with zero cost for local models (e.g. Ollama `qwen2.5-vl:3b`) and token-based cost tracking via `model.Price` for latest Gemini Flash-Lite models (`gemini-2.5-flash-lite`, `gemini-3.1-flash-lite`).
- **Dedicated Mistral OCR (`MistralOCREngine`).** Stdlib HTTP integration with Mistral's `mistral-ocr-latest` API, providing structured Markdown and per-page cost tracking with user-supplied rates.
- **Scanned page vs. figure separation.** Full-page background scans and multi-strip scan tiles are classified as `Page.ScanImage` (`IsPageScan = true`), suppressing false markdown figure placeholders (`![Figure on page 1](...)`) while preserving embedded illustrations in `Page.Images`.
- **Cost and usage tracking.** `Page` and `Document` track `Usage model.Usage` and `Cost float64` across all OCR calls without hardcoded price tables.
- **Strict error propagation.** Upstream OCR rejections, network errors, and context cancellations now propagate as typed/wrapped errors rather than being silently dropped.

## v0.8.3 — 2026-09-19

This patch release generalizes PDF table extraction in `package pdfextract`, eliminating empty and spurious tables from vector graphics and diagrams, while improving multi-column and multi-line row reconstruction for Booktabs and stream tables.

### Fixed

- **Suppressed empty and spurious table grids in PDF extraction.** Discards empty vector grids (such as chart axes, plot frames, and subfigure borders) that contain no text, preventing tables with dummy headers (`Col 1 | Col 2`) and empty rows from polluting Markdown output.
- **Enhanced Booktabs table reconstruction.** Accurately groups text spans by horizontal baseline bands and clusters column start coordinates. Multi-line headers (e.g. above `\midrule`) and multi-line wrapped cells are now unified into clean, coherent table records instead of being split into fragmented single-cell rows.
- **Phantom empty column and row pruning.** Automatically removes empty columns caused by vector dividers and tick marks, and trims empty border rows while updating bounding boxes.
- **Table block formatting.** Guarantees proper blank-line separation before Markdown tables so standard Markdown renderers (such as `remark-gfm`) parse tables correctly.

## v0.8.1 — 2026-09-19

This release ships high-performance, multi-format document extraction
(`package docextract` and the `extract_doc` common tool) supporting Word
(.docx), Excel (.xlsx), PowerPoint (.pptx), Markdown (.md), CSV (.csv),
TSV (.tsv), and plain text documents alongside PDF routing.

### Added

- **Multi-format document extraction.** `package docextract` and the
  `extract_doc` common tool extract clean, structured Markdown from Word
  documents (.docx), Excel spreadsheets (.xlsx), PowerPoint presentations
  (.pptx), Markdown specifications (.md), CSVs, and text files. The
  implementation is pure Go with zero external dependencies (using standard
  library `archive/zip`, `encoding/xml`, and `encoding/csv`). It preserves
  headings, bulleted/numbered lists, inline text styles (bold, italic,
  strike, monospace), external hyperlinks, intact GitHub-Flavored Markdown
  tables (including nested tables), slide-by-slide speaker notes, and
  worksheet grids.
- **Outline and structural navigation.** `docextract` supports `outline: true`
  mode for hierarchical tables of contents, slide lists, and sheet inventories
  without loading full document bodies, protecting agent context budgets.
- **Section and query filtering.** Models can query specific heading sections
  (`query: "Installation"`), slide titles, or filter spreadsheet/CSV rows
  before token consumption.
- **Unified document tool routing.** PDFs are automatically routed to
  `pdfextract` while preserving security confinement inside the configured
  root directory.

## v0.8.0 — 2026-09-19

This release ships three major capabilities: high-performance layout-aware
PDF extraction (`package pdfextract`), explicit sanitization for histories
crossing untrusted boundaries (`golem.SanitizeHistory`), and deliberate
in-tool run cancellation (`&tool.Canceled{Reason}`). All changes are opt-in
and additive; existing call sites and serialized message JSON are unchanged.

### Added

- **High-performance layout-aware PDF extraction.** `package pdfextract`
  and the `extract_pdf` common tool extract structured Markdown from PDF
  documents in milliseconds without Python, Tesseract, or LLMs. Features
  include recursive XY-cut multi-column sequencing, geometric fraction
  reconstruction (`\frac{...}{...}`), display math detection (`$$ ... \tag{N} $$`),
  subscript and superscript baseline attachment, intact table extraction (Lattice and
  Booktabs), high-resolution image extraction with spatial author-caption
  proximity matching (`model.PartImage`), and pluggable OCR fallback (`OCREngine`).
  Inherited page tree attributes, page rotation (`/Rotate`), standard
  non-Unicode encodings (`/WinAnsiEncoding`, `/Differences`), and Adobe Glyph
  List mappings are supported natively.

- **Untrusted-history sanitization.** `golem.SanitizeHistory` is the
  explicit pass for history that arrived over a trust boundary — a
  browser request resuming a conversation, a client-submitted paused
  run: it drops system messages, drops URL parts whose scheme is not
  http or https (the one wire-visible gap this closes; inline data is
  untouched), repairs call/result pairing, and reports every change —
  `SanitizeReport.SystemPrompts`, `UnsafeParts` (message index, kind,
  rejected scheme), and the pairing `Repair` — so an endpoint can log,
  reject, or bill for what a client tried to assert. The pass never
  rewrites content, is deterministic and idempotent, and never
  mutates the input. Runs never sanitize automatically: the trust
  boundary is the application's, and the guide now states the
  accompanying rules — transport authentication, per-caller toolsets,
  server-side re-validation of high-stakes effects (decision in ADR
  0029).

- **In-tool run cancellation.** A tool can now end the run
  deliberately: returning `&tool.Canceled{Reason}` from `Exec` stops
  the run at the new `canceled` stage — not a failure, no retry budget
  touched — with the sentinel reachable through `RunError` via
  `errors.As` and the evidence on `RunError.Partial`. Inside the batch,
  calls before the stop keep their recorded results; the stopping call
  and everything after it are closed with the synthesized no-result
  result, so the transcript resumes through `RunWithHistory` without
  repair. Later calls never start; a pending pause is discarded in
  favor of the stop. A delegated sub-agent that cancels cancels the
  delegating run, and an approved deferred re-run that returns the
  sentinel ends the resume run before any model call. Observers gain
  the additive `EventCanceled` boundary marker (decision in ADR 0028).

## v0.7.6 — 2026-09-11

This release completes six items of the pre-v1 feature series: a run
can be priced before it is sent and after it completes, tools can
return media and fail definitively, damaged histories can be
normalized explicitly, and every run and conversation carries an
identity — with the test suite hardened around the durable message
contract while all of it landed. One breaking change ships, compiler-
enforced and mechanical (the tool `Exec` signature).

### Added

- **Token counting.** A provider-neutral `tokens.Counter` port prices a
  request before it is sent: adapters ship where the provider offers a
  counting endpoint — `anthropic.Counter`, `gemini.Counter`, and
  `bedrock.Counter`, each reusing that adapter's request builders — and
  applications implement the port where none exists (decision in ADR
  0023). Two users ride the port: `UsageLimit.PerRequestInputTokens`
  with `golem.WithTokenCounter` fails a run at the usage stage before
  the oversized request goes out, and `golem.BudgetHistory` bounds
  history by token budget as the counting sibling of `TrimHistory`.
  `testmodel.CountFunc` fakes the port offline.

- **Cost.** A `model.Price` port turns provider-reported usage into
  dollars: each generation adapter ships a `Price` struct with
  per-million-token rates encoding how its own usage fields combine
  (cached input inside the total on OpenAI-compatible APIs and Gemini,
  beside it on Anthropic and Bedrock), applications implement the port
  for negotiated or proxied pricing, and no price table ships (decision
  in ADR 0024). `golem.WithPrice` feeds `Result.Cost` and
  `PartialResult.Cost`, and `UsageLimit.Cost` enforces a post-response
  cost bound — the bound without a price fails construction.

- **Tool-result parts and definitive tool failures.** `tool.Tool.Exec`
  returns a `tool.Result` — result text plus `model.Part` evidence —
  so a tool can hand the model the image or document it produced
  (`tool.Text` migrates text-only tools; decision in ADR 0025).
  Adapters place the parts where their API carries them: inside the
  tool result on Anthropic and Bedrock, framed onto one attributed
  user message on OpenAI, Azure, and Gemini; an adapter that cannot
  carry a kind fails before the request rather than dropping it.
  `&tool.Failed{Reason}` records a definitive failure as the tool's
  result on a `model.Message` flagged `Failed` — the model sees it,
  the run continues, and no retry budget is consumed.

- **History normalization.** `golem.NormalizeHistory` runs the pairing
  pass the request builder uses — synthesized interrupted results for
  unanswered calls, orphaned results dropped — and reports every change
  (decision in ADR 0026). It also detects tool calls whose arguments
  are not a valid JSON object; the bytes stay verbatim in the history,
  and every adapter now serializes such a call as
  `{"truncated_args": "<verbatim bytes>"}` on the wire instead of
  failing to encode or sending a request the provider rejects.

- **Run and conversation identity.** Every run carries a `RunID` —
  minted fresh per run, never inherited, overridable with
  `golem.WithRunID` — stamped on its events, its `Result`, and every
  message it adds to the conversation; `RunWithHistory` chains run into
  one conversation under a shared `ConversationID`, inherited from the
  supplied history's most recent identified message, pinnable or
  forkable with `golem.WithConversationID`, and minted by
  `golem.NewID()` (a time-ordered UUID version 7) otherwise. Both ride
  the durable message JSON as additive `runId` and `conversationId`
  fields, so the association survives storage with no session object
  (decision in ADR 0027).

### Changed

- **`tool.Tool.Exec` now returns `(tool.Result, error)` instead of
  `(string, error)`.** Wrap the returned string with `tool.Text(s)` to
  migrate; the compiler finds every call site. See the Added entry on
  tool-result parts.
- **`UsageLimitError.Limit` and `.Actual` are now `float64`.** One error
  shape covers every bounded dimension, and cost bounds are dollar
  amounts; integer literals at existing call sites are unaffected.
  Error text renders bounds with `%g`.
- **Test hardening.** The durable message-JSON contract carries a Go
  fuzz target (`model.FuzzMessageJSONIsDurable`) whose seed corpus runs
  in every `go test` and whose continuous fuzzing runs bounded in CI;
  CI gains a `-race` test job, runs every `examples/` program requiring
  exit 0 without credentials, and `scripts/smoke.sh` drives the
  adapters' opt-in live tests as one matrix, skipping adapters whose
  `GOLEM_*` keys are unset.

## v0.7.5 — 2026-09-08

This patch widens what a run can take in and adds the retrieval
primitive beside it: multimodal input grows beyond images to documents,
audio, and video, and a provider-neutral embeddings port embeds queries
and corpora for applications building search on Golem.

### Added

- **Document, audio, and video input parts.** The multimodal part
  vocabulary grows beyond images: `model.PartDocument`, `PartAudio`,
  and `PartVideo` join `PartImage` on `model.Message.Parts`, with
  constructors `model.DocumentURL`, `model.DocumentData`,
  `model.AudioData`, and `model.VideoData` and the same validation,
  durable JSON, and evidence semantics as image parts (decision in
  ADR 0022). Each adapter maps the kinds its provider accepts —
  OpenAI-compatible endpoints take inline PDF documents and wav/mp3
  audio, Anthropic takes PDF documents by URL or inline, Gemini takes
  all four kinds inline or behind provider-addressable URLs, Bedrock
  takes inline documents in the pdf, csv, Office, html, txt, and md
  formats — and rejects unsupported combinations before any request
  with an error naming the fix. No new run options: `WithPromptParts`
  carries every kind.

- **Embeddings.** A provider-neutral `embedding.Embedder` port with the
  query/documents split as the task-type encoding: `EmbedQuery` embeds
  one search query, `EmbedDocuments` embeds a corpus batch in one
  provider call, and both return `embedding.Result` — vectors in input
  order plus `model.Usage` input-token evidence. Adapters ship where
  the provider offers an embeddings API — `openai.Embedder`
  (OpenAI-compatible endpoints, including Ollama and LM Studio through
  `BaseURL`), `azure.Embedder` (deployment-addressed Azure OpenAI), and
  `gemini.Embedder` (retrieval task types, `outputDimensionality`) —
  each with config-level `Dimensions` truncation where the provider
  supports it and the shared `APIError`/`TransportError`/`DecodeError`
  classification. `testmodel.EmbedFunc` fakes the port offline. Vector
  stores, chunking, and rerankers stay application concerns; Anthropic
  has no embeddings API (decision in ADR 0021).

## v0.7.4 — 2026-09-06

This patch makes cache economics first-class: a run reports the
provider's token breakdown, and the adapters expose prompt caching
where the provider offers control, so applications can see — and
shape — what caching saves.

### Added

- **Prompt caching (Anthropic, Bedrock).** The adapters expose prompt
  caching where the provider offers control, with a shared shape:
  `anthropic.Config.CacheControl` sends Anthropic's automatic-caching
  parameter (breakpoint on the last cacheable block, moving forward as
  the conversation grows), and `bedrock.Config.CacheControl` places an
  explicit `cachePoint` checkpoint at each request's conversation
  frontier on Bedrock. A zero TTL selects the five-minute default on
  both; `anthropic.CacheOneHour` / `bedrock.CacheOneHour` ask for the
  one-hour entry. OpenAI, Azure, and Gemini cache implicitly with
  nothing to send. Cache hits and writes stay visible on the usage
  detail fields, so a run reports what caching saved.

- **Usage detail.** `model.Usage` gains provider-reported
  `CacheReadTokens`, `CacheWriteTokens`, and `ReasoningTokens` beside
  the token totals, captured from every adapter on streamed and plain
  runs alike and summed across a run's turns on the result — the raw
  material a cost ledger needs to price cache discounts, cache-write
  premiums, and reasoning output, where the provider breaks them out.
  Detail figures are zero when unreported; the providers guide tables
  which field each adapter reports and whether it is a subset of the
  totals or exclusive of them.

## v0.7.3 — 2026-09-06

This patch completes run evidence on the success path: a finished run
now reports what it did and why the model stopped, matching what
failures already preserved.

### Added

- **Finish-reason visibility.** Every response now carries the
  provider's terminal cause, normalized onto `model.FinishReason`
  (`stop`, `length`, `tool_call`, `content_filter`, `other`) from all
  five adapters, on streamed and plain runs alike. `Result.FinishReason`
  holds the final turn's cause and `RunError.Partial.FinishReason` the
  last completed turn's, so a response the provider truncated — the
  classic undecodable-JSON failure — is diagnosable instead of silent.
  Bedrock's stream adapter also now tolerates a nil fragment callback,
  matching its sibling adapters.

- **Run activity counts on the result.** `Result.Requests` and
  `Result.ToolCalls` surface the model requests — retried attempts
  included — and the tool executions a successful run performed: the
  same counts the usage limit enforces and `RunError.Partial` preserves
  on failure, so a cost ledger reads them off the result instead of
  inferring activity from messages. Paused runs carry them too, and the
  counts cover one run only: a delegated sub-agent's activity stays in
  the sub-agent's own result.

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
