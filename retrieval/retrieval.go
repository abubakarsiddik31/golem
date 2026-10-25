// Package retrieval stores embedded documents and answers top-k
// queries — the search half of retrieval-augmented generation, beside
// the embedding port that converts text to vectors. The application
// embeds and chunks (the Embedder's contract keeps both there); this
// package stores and ranks. One offline implementation, Memory, ships
// here; vector databases stay application-side behind the same
// interface.
//
// The port follows the module's rule for optional capability: an
// interface the application implements, never a dependency it must
// take. See docs/adr/0040-retrieval.md.
package retrieval

import (
	"context"
	"fmt"
)

// Document is one searchable text with its identity and metadata. The
// application chooses IDs; an index keyed by them replaces on re-add,
// so re-indexing a changed document is idempotent.
type Document struct {
	// ID identifies the document within one index. Empty is valid but
	// weak: ties are broken by ID, and replacement is by ID.
	ID string
	// Text is the retrievable content, typically one chunk of a larger
	// document.
	Text string
	// Metadata carries application-side facets — source path, section
	// title, URI — returned with every hit, never searched.
	Metadata map[string]string
}

// Embedded is one document and the vector an Embedder produced for its
// text.
type Embedded struct {
	Document  Document
	Embedding []float32
}

// Hit is one search result: the document and its cosine similarity to
// the query, in [-1, 1] with 1 an exact direction match.
type Hit struct {
	Document Document
	Score    float64
}

// Index stores embedded documents and answers top-k queries. An Index
// must be safe for concurrent use when its implementation is; Memory
// is.
type Index interface {
	// Add stores or replaces documents by ID, with their embeddings, in
	// one call. Every embedding must have the index's dimensionality.
	Add(ctx context.Context, docs []Embedded) error
	// Search returns up to k documents nearest to the query vector by
	// cosine similarity, best first. Ties break by document ID
	// ascending, so equal scores order deterministically.
	Search(ctx context.Context, query []float32, k int) ([]Hit, error)
}

// DimensionError reports a vector whose width does not match the
// index's.
type DimensionError struct {
	// Expected is the index's dimensionality.
	Expected int
	// Got is the offending vector's width.
	Got int
	// ID is the document ID on an Add, empty on a Search.
	ID string
}

func (e *DimensionError) Error() string {
	if e.ID != "" {
		return fmt.Sprintf("retrieval: document %q: embedding has %d dimensions, index expects %d", e.ID, e.Got, e.Expected)
	}
	return fmt.Sprintf("retrieval: query embedding has %d dimensions, index expects %d", e.Got, e.Expected)
}
