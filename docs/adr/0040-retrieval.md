# ADR 0040: Retrieval — an index port, an in-memory implementation, and a search tool

## Status

Accepted. Companion to the embeddings port (ADR 0021).

## Context

The embeddings port shipped the conversion half of retrieval-augmented
generation: text in, vectors out, queries and documents separated
because retrieval models condition the two sides differently. What it
deliberately did not ship was the search half — somewhere to put the
vectors and rank queries against them.

Every framework in the comparison set owns that half one level down:
eino has retriever and indexer components, and the Python ecosystem
assumes a vector store. Without it, every Golem application that
answers from a corpus rewrites the same top-k cosine loop and the same
"give the agent a search tool" plumbing, and each rewrites the subtle
parts slightly wrong: tie-breaking, zero vectors, dimension
validation, snapshot semantics.

The module's constraints are unchanged: standard library only, no
dependency on any vector database, and additive exported surface. A
first-party pgvector or Pinecone client is ruled out on sight; the
question is how much of the middle Golem owns.

## Decision

Three pieces, one package.

**The port.** `retrieval.Index` is two methods: `Add(ctx, []Embedded)`
stores or replaces documents by ID with their vectors, and
`Search(ctx, queryVector, k)` returns up to k hits by cosine
similarity, best first, ties broken by document ID ascending so equal
scores order deterministically. `Document` is ID, text, and
application-owned metadata; `Hit` returns the document with its score.
A dimension mismatch is a typed `*DimensionError`, so a misconfigured
pipeline fails loudly instead of ranking garbage. Value semantics:
implementations snapshot, because callers reuse their slices.

**The offline implementation.** `retrieval.Memory` is a linear-scan
cosine index over `[]float32` — the embeddings port's vector type —
with an `RWMutex`, float64 accumulation, and a defined zero rule: a
zero-magnitude side scores 0 rather than NaN, so an all-zero embedding
can rank only at the bottom. It is honest about its range: tens of
thousands of chunks, not millions. Beyond that, a vector database
implements `Index`, and the tool and application code above it do not
change — that is what the port is for.

**The tool.** `retrieval.MustNew(ToolConfig{Index, Embedder, K})`
builds a `search_documents` tool. Exec validates the model's query as
a correctable `*model.ModelRetry`, embeds the query through the same
`embedding.Embedder` that produced the stored vectors, searches, and
renders the hits as ranked text with IDs, scores, and metadata. A
provider embedding failure is a plain error — a quota problem is not a
correctable model mistake — and an empty result renders as an honest
"no documents matched" rather than an empty string.

The application keeps chunking, re-indexing policy, and backend
choice. Golem owns the ranking semantics and the agent-facing seam.

## Alternatives considered

**No package; a recipe in the embeddings guide.** It is what the
module had. The loop is short but the rules around it — determinism,
zero vectors, typed dimensions, snapshot-on-add, the model-retry
boundary — are exactly what each application gets subtly wrong, and
the agent-facing tool is plumbing nobody should rewrite.

**A richer port.** Metadata filtering, hybrid scoring, incremental
delete, persistence, iterators. Each is backend policy with a
different right answer per store, and each would grow the contract
toward a database. Two methods cover the conversation lifecycle of an
index — put documents in, get matches out.

**float64 vectors.** The embeddings port already speaks `[][]float32`,
the providers' native width. Converting at the boundary would double
the memory for nothing; ranking accumulates in float64 regardless, so
precision where it matters is kept.

**A separate RAG pipeline type** (chunker + embedder + index + tool
composed by Golem). Chunking quality is corpus-specific — section
boundaries, overlap, size — and ADR 0021 placed it with the
application deliberately. A pipeline type would re-open that decision
while hiding a provider-shaped quality trade-off inside Golem.

**Named `Vector`/`Store`.** The type is a text index over embedding
vectors; "vector store" is database vocabulary for what here is one
in-memory implementation. Naming the port `Index` and the vector
fields plainly keeps the door open for backends without borrowing
their vocabulary.

## Consequences

A knowledge base is three steps in application code — chunk, embed,
add — and one option on the agent: `retrieval.MustNew(...)` in
`WithTools`. The example runs offline with a stand-in embedder and
swaps to a real one by replacing a single interface value.

Scores are cosine similarities, comparable only within one embedding
model. Mixing models between Add and the tool's Embedder is a
configuration error that produces confident nonsense; the tool's
config documentation says the two must be one model, and the typed
dimension error catches the width mismatch that usually accompanies
it.

`Memory` holds everything in the process and persists nothing. An
application that needs durability wraps its store in an `Index`
implementation — the guide shows the interface — and the tool keeps
working unchanged.

If a real backend integration earns its place later — a pure-Go
sqlite-vec-style store, say — it lands as an implementation of this
port, additively. The seam, not the backend, was the decision.
