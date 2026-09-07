// Package embedding defines the provider-neutral contract for text
// embedding services: turning text into dense vectors for semantic
// search, clustering, and retrieval-augmented generation. Provider
// adapters live under providers; vector stores, chunking, and
// rerankers stay application concerns.
package embedding

import (
	"context"

	"github.com/abubakarsiddik31/golem/model"
)

// Embedder converts text into embedding vectors. Queries and documents
// are separate calls because retrieval models condition the two sides
// differently — providers that expose a task type receive the split as
// that type, so embedding corpus documents as queries degrades quality
// rather than erroring. Implementations must honor ctx cancellation and
// return errors rather than logging them as a substitute for
// propagation.
type Embedder interface {
	// EmbedDocuments embeds a corpus batch in input order, one provider
	// call. Texts must be non-empty; chunking and splitting stay with
	// the application.
	EmbedDocuments(ctx context.Context, texts []string) (Result, error)
	// EmbedQuery embeds a single search query; the result carries exactly
	// one vector.
	EmbedQuery(ctx context.Context, text string) (Result, error)
}

// Result is one embedding call's evidence: the vectors in input order
// and the provider-recorded usage. Embeddings bill input only, so
// Usage.InputTokens carries the consumed tokens and the remaining
// fields are zero — including zero when a provider reports no usage.
type Result struct {
	// Vectors holds one embedding per input, in input order. Each vector
	// is non-empty; its width is fixed by the model and the configured
	// dimensions.
	Vectors [][]float32
	// Usage reports provider-recorded consumption in the shared
	// vocabulary.
	Usage model.Usage
}
