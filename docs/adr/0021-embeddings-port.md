# ADR 0021: Embeddings port

## Status

Accepted.

## Context

Retrieval applications built on Golem — the maintainer's own RAG
service among them — need text embeddings: fixed-width vectors that
turn semantic search, deduplication, and retrieval-augmented prompts
into ordinary Go code. Today they reach for raw provider SDKs or
hand-rolled HTTP for the one call Golem does not cover, which forfeits
the framework's explicit configuration, typed error classification,
and offline testability. Every major provider in Golem's adapter set
serves embeddings over a batch-oriented HTTP API — OpenAI
(`/v1/embeddings`, which Azure and local runtimes re-address),
Gemini (`models/{model}:embedContent` and `:batchEmbedContents`) —
except Anthropic, which has no embeddings API. Upstream ships the
capability as an `Embedder` with an `embed_query`/`embed_documents`
split, an `EmbeddingResult` carrying usage, per-provider settings, and
a test double.

The wire vocabularies are parallel but not identical. OpenAI takes one
`model` plus an array of up to 2048 input strings (8192 tokens each)
and reports `usage.prompt_tokens`; optional `dimensions` shrinks
`text-embedding-3` outputs. Gemini embeds one text per
`EmbedContentRequest` or a list via `BatchEmbedContents`, and
conditions the vector on a `taskType` — retrieval queries and corpus
documents embed differently — with `outputDimensionality` the same
shrink knob and `usageMetadata.promptTokenCount` the same usage
evidence. The query/document asymmetry is the embedding world's
task-type encoding: retrieval quality depends on embedding the query
and the documents on opposite sides of the split.

## Decision

Embeddings ship as a small provider-neutral port in a new `embedding`
package, mirroring how `model` carries the generation contract:

- `embedding.Embedder` is a two-method interface —
  `EmbedQuery(ctx, text) (Result, error)` for the search side and
  `EmbedDocuments(ctx, texts) (Result, error)` for the corpus side.
  The split is the port's only task-type encoding; adapters translate
  it per provider (Gemini's `RETRIEVAL_QUERY`/`RETRIEVAL_DOCUMENT`,
  where OpenAI-compatible APIs ignore it). A single
  method-plus-kind-enum was rejected: the asymmetric-retrieval mistake
  — embedding documents as queries — becomes inexpressible rather than
  merely discouraged, and call sites read like the domain. Context is
  first; implementations must honor cancellation.
- `embedding.Result` carries `Vectors [][]float32` (input order,
  length matching the inputs) and `Usage model.Usage` — the same
  vocabulary as generation, so a cost ledger reads one shape.
  Embeddings bill input only: `InputTokens` is populated and the
  remaining fields are zero, including for providers that report no
  usage at all. `float32` matches every provider's wire floats and the
  Go vector ecosystem; upstream's `float64` vectors double memory for
  no fidelity gain.
- Adapters follow the generation-client conventions: `Embedder`
  clients in `providers/openai`, `providers/azure`, and
  `providers/gemini`, each with an explicit `EmbedderConfig`, a
  validating `NewEmbedder`, stdlib-only transport, the shared
  `APIError`/`TransportError`/`DecodeError` triad, and a `Dimensions`
  config field that rides the request only when set. Empty input lists
  and empty strings fail before any network call. One batch call per
  `EmbedDocuments` — chunking past provider batch limits stays an
  application concern, like chunking itself.
- Applications substitute implementations (tests, custom runtimes), so
  the interface earns its place; `testmodel.EmbedFunc` adapts a
  function to the port, matching the `testmodel.Func` pattern.

Rejected: port methods returning bare vectors (loses usage evidence),
a settings-merging layer per call (config-level `Dimensions` covers
the demonstrated need), a task-type override on the port (the split is
the encoding; other Gemini task types await demand), client-side
concurrency for per-item APIs (both shipped adapters batch natively),
and rerankers, chunkers, or vector stores (application concerns, per
the roadmap's non-goals).

## Consequences

A retrieval pipeline becomes two calls with one vocabulary — embed
documents once into an index, embed each query as it arrives — with
offline tests through the fake. The interface is additive to the core
run loop, which never touches embeddings; nothing about `Agent`,
`Result`, or history changes. Each new provider embedding API means
one more adapter, and providers without one (Anthropic) remain
out of scope. Usage-based cost accounting inherits the same
`model.Usage` caveat as generation: providers that omit usage report
zeroes.
