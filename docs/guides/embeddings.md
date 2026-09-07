# Embeddings

## Purpose

Turn text into dense vectors through a provider-neutral `embedding.Embedder`
port, so semantic search, clustering, and retrieval-augmented generation
stay ordinary Go code with Golem's explicit configuration, typed errors,
and offline testability. Decided in ADR 0021.

## When to use

Use it whenever an application needs embeddings next to an agent that runs
on Golem — building a retrieval index, embedding queries as they arrive,
deduplicating or clustering text. Do not use it to run agents: embeddings
never enter the run loop, and a run result never carries vectors. Vector
stores, document chunking, and rerankers stay application concerns; the
port only turns text into vectors and reports usage.

## How it works

The port separates the two sides of a retrieval pair, because retrieval
models condition them differently:

- `EmbedQuery(ctx, text)` embeds one search query. Adapters that expose a
  task type send the query side — Gemini receives `RETRIEVAL_QUERY`.
- `EmbedDocuments(ctx, texts)` embeds a corpus batch in one provider call,
  returning one vector per input in input order — Gemini receives
  `RETRIEVAL_DOCUMENT`; OpenAI-compatible endpoints have no task type and
  ignore the split.

Both return the same `embedding.Result`: `Vectors` (`[][]float32`, one
non-empty vector per input) and `Usage` — the same `model.Usage`
vocabulary as generation. Embeddings bill input only, so `InputTokens`
carries the consumed tokens and the other fields are zero; a provider
that reports no usage leaves it zero too. Embedding a document batch as
queries does not error — it silently degrades retrieval quality, which is
why the split is the port's shape.

Texts must be non-empty; empty batches and empty strings fail before any
network call with a `DecodeError`. Very large batches must be chunked by
the application: OpenAI-compatible APIs cap one request at 2048 inputs of
8192 tokens each. Context cancellation always wins and surfaces as a
non-retryable `TransportError`; provider failures return the adapter's
`*APIError` (retryable for 408, 429, and 5xx, like generation), and
response-shape violations — vector counts that disagree with the inputs,
empty vectors, malformed JSON — return `*DecodeError`.

## Example

The runnable path is `examples/embeddings` (`go run ./examples/embeddings`
with `OPENAI_API_KEY` set, or with `OPENAI_BASE` pointed at a local
OpenAI-compatible runtime): embed three documents, embed a query, and
rank by cosine similarity computed in the example — a vector store stood
in by a sort. Inline, the whole pipeline is two calls:

```go
embedder, err := openai.NewEmbedder(openai.EmbedderConfig{
	APIKey: apiKey,
	Model:  "text-embedding-3-small",
})
if err != nil {
	return err
}
docs, err := embedder.EmbedDocuments(ctx, chunks)
if err != nil {
	return err
}
// ... store docs.Vectors in the index, later:
query, err := embedder.EmbedQuery(ctx, userQuestion)
if err != nil {
	return err
}
// ... nearest neighbors of query.Vectors[0], then hand the text to an agent.
```

## API surface

- `embedding.Embedder` — the port: `EmbedDocuments(ctx, texts) (Result, error)` and `EmbedQuery(ctx, text) (Result, error)`.
- `embedding.Result` — `Vectors [][]float32` in input order plus `Usage model.Usage`.
- `openai.EmbedderConfig` / `openai.NewEmbedder` / `openai.Embedder` — OpenAI-compatible embeddings; `BaseURL` covers local runtimes such as Ollama and LM Studio.
- `azure.EmbedderConfig` / `azure.NewEmbedder` / `azure.Embedder` — Azure OpenAI embeddings via a deployment (`Endpoint`, `Deployment`, `APIVersion`).
- `gemini.EmbedderConfig` / `gemini.NewEmbedder` / `gemini.Embedder` — Gemini embeddings (`embedContent`, `batchEmbedContents`).
- `testmodel.EmbedFunc` — adapts `func(ctx, texts, query bool) (embedding.Result, error)` to the port for tests.

## Gotchas

- Embedding models are not interchangeable: switching models (or
  dimension settings) changes the vector space and width, so an existing
  index must be re-embedded. Pick dimensions once and store them beside
  the index.
- `Dimensions` truncates output width only where the provider supports
  it — OpenAI `text-embedding-3` and later, Gemini's
  `outputDimensionality` on newer models, and Ollama's `dimensions`. On
  models without support the provider rejects or ignores it; zero means
  the provider default everywhere.
- The adapters never split batches. OpenAI-compatible APIs fail a request
  past 2048 inputs; Gemini's batch endpoint has no fixed documented cap
  but very large batches still want application-side chunking for
  latency and retry granularity.
- Anthropic has no embeddings API, so there is no Anthropic embedder;
  pair a Golem agent with any of the three embedders above.
- Usage is provider-reported and providers disagree: OpenAI-compatible
  endpoints return `usage.prompt_tokens`, but Gemini's embedding
  endpoints return no usage metadata today, so `Result.Usage` stays
  zero there (verified on the wire, September 2026) even though the
  API contract reserves the field.
- Azure embeddings address a deployment, not a model: the deployment
  rides the URL and no model field rides the body. API keys go in the
  `api-key` header, as with the chat adapter.
- Guide to ADR 0021 for the rejected alternatives (bare-vector returns,
  a settings-merging layer, task-type overrides beyond the split).
