# Retrieval

## Purpose

Store embedded documents and answer top-k queries — the search half of
retrieval-augmented generation — through a two-method port, with an
offline in-memory implementation and a ready-made agent tool.

## When to use

When an agent should answer from a corpus that is not in the prompt:
product docs, policies, tickets, code. You already have an
`embedding.Embedder`; this package stores what it produces and ranks
queries against it. When retrieval should stay out of the model's
hands — you always know which passages a task needs — call the index
directly instead of registering the tool. Large-scale or persistent
corpora belong in a real vector database: it implements the same
`retrieval.Index` interface and the rest of the application is
unchanged.

## How it works

Chunk your corpus (your job — section boundaries and overlap are
corpus-specific), embed the chunks with `EmbedDocuments`, and add them
with their vectors:

```go
index := retrieval.NewMemory()

embeddings, err := embedder.EmbedDocuments(ctx, texts)
// one retrieval.Embedded per chunk: Document{ID, Text, Metadata} + Embedding
err = index.Add(ctx, embedded)

hits, err := index.Search(ctx, queryVector, 5) // best first
```

`Search` ranks by cosine similarity, ties broken by document ID, so
equal scores order deterministically. A zero vector scores 0 rather
than NaN. Dimension mismatches fail with `*retrieval.DimensionError` —
the usual symptom of indexing and querying with different embedding
models. `Add` replaces by ID, so re-indexing a changed document is
idempotent. Implementations snapshot: mutating your slices afterwards
does not touch the index, and hits carry copied metadata.

As an agent tool, the same index becomes one option:

```go
agent, _ := golem.New[struct{}, string](client, decoder,
	golem.WithTools[struct{}, string](
		retrieval.MustNew[struct{}](retrieval.ToolConfig{
			Index:    index,
			Embedder: embedder, // the same model that embedded the corpus
			K:        5,
		}),
	),
)
```

The `search_documents` tool takes `{"query": string}`, embeds the
query with `EmbedQuery`, and returns the hits as ranked text — ID,
score, text, metadata — best first. Malformed arguments are correctable
`*model.ModelRetry`; an embedding provider failure is a plain error,
because a quota problem is not a model mistake.

## Example

Run `examples/retrieval` — offline: a deterministic keyword stand-in
plays the embedder, a four-section policy corpus is indexed, and the
model searches it as a tool to answer a support question. Swap the
stand-in for a real embedder to go live.

## API surface

- `retrieval.Index` — `Add(ctx, docs []Embedded) error`, `Search(ctx, query []float32, k int) ([]Hit, error)`
- `retrieval.NewMemory() *Memory` — the offline in-memory cosine index; `Len()` reports its size
- `retrieval.Document{ID, Text, Metadata}`, `retrieval.Embedded{Document, Embedding}`, `retrieval.Hit{Document, Score}`
- `retrieval.DimensionError{Expected, Got, ID}` — the typed width mismatch
- `retrieval.New[Deps](retrieval.ToolConfig{Index, Embedder, K}) (tool.Tool[Deps], error)`, `retrieval.MustNew[Deps](...)` — the `search_documents` tool
- `retrieval.ToolName`, `retrieval.DefaultK` — `search_documents`, 5

## Gotchas

- Index and tool must share one embedding model. Cosine scores are
  only comparable within a model; the dimension error catches width
  mismatches, but two models with the same width fail silently and
  confidently.
- Chunks, not whole documents. The embedder's contract says chunking
  is yours; a 50-page PDF embedded as one vector retrieves as one
  blob.
- `Memory` is linear-scan and in-process: right for tens of thousands
  of chunks, wrong for millions or for multi-service access. Implement
  `Index` against your vector database; the tool does not change.
- Metadata is returned verbatim with every hit and rendered into the
  tool's output — do not put secrets there.
- Re-adding an ID replaces the document. That is the update path; it
  is not incremental — search sees the whole replacement.
- Scores are similarities in [-1, 1], not percentages; 0 means
  unrelated, not absent. Threshold in your own pipeline if you filter.
- The design — two-method port, the zero rule, the tool's retry
  boundary — is ADR 0040.
