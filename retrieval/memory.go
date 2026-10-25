package retrieval

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
)

// Memory is an in-process Index: cosine similarity over the stored
// vectors, a linear scan per query, deterministic tie-breaking. It
// serves corpora up to tens of thousands of chunks; beyond that, a
// vector database implements Index and the rest of the application —
// including the search tool — is unchanged.
type Memory struct {
	mu    sync.RWMutex
	dim   int
	docs  map[string]Embedded
	order []string // insertion order of IDs, for deterministic output
}

// NewMemory returns an empty in-memory index. Its dimensionality is
// fixed by the first added embedding.
func NewMemory() *Memory {
	return &Memory{docs: map[string]Embedded{}}
}

// Add stores or replaces documents by ID. The first embedding fixes
// the index's dimensionality; every other must match it.
func (m *Memory) Add(_ context.Context, docs []Embedded) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, doc := range docs {
		if len(doc.Embedding) == 0 {
			return fmt.Errorf("retrieval: document %q: embedding is empty", doc.Document.ID)
		}
		if m.dim == 0 {
			m.dim = len(doc.Embedding)
		}
		if len(doc.Embedding) != m.dim {
			return &DimensionError{Expected: m.dim, Got: len(doc.Embedding), ID: doc.Document.ID}
		}
		// Snapshot the document and its vector: the caller owns its
		// slices and may reuse them.
		stored := Embedded{Document: doc.Document, Embedding: append([]float32(nil), doc.Embedding...)}
		if stored.Document.Metadata != nil {
			stored.Document.Metadata = cloneMetadata(stored.Document.Metadata)
		}
		if _, exists := m.docs[doc.Document.ID]; !exists {
			m.order = append(m.order, doc.Document.ID)
		}
		m.docs[doc.Document.ID] = stored
	}
	return nil
}

// Search returns up to k hits nearest to the query vector by cosine
// similarity, best first, ties by document ID ascending. A k larger
// than the corpus returns everything; k must be positive.
func (m *Memory) Search(_ context.Context, query []float32, k int) ([]Hit, error) {
	if k <= 0 {
		return nil, fmt.Errorf("retrieval: k must be positive, got %d", k)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(query) == 0 {
		return nil, fmt.Errorf("retrieval: query embedding is empty")
	}
	if m.dim != 0 && len(query) != m.dim {
		return nil, &DimensionError{Expected: m.dim, Got: len(query)}
	}

	hits := make([]Hit, 0, len(m.order))
	for _, id := range m.order {
		stored := m.docs[id]
		hits = append(hits, Hit{Document: cloneDocument(stored.Document), Score: cosine(query, stored.Embedding)})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Document.ID < hits[j].Document.ID
	})
	if len(hits) > k {
		hits = hits[:k]
	}
	return hits, nil
}

// Len reports the number of documents in the index.
func (m *Memory) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.docs)
}

// cosine returns the cosine similarity of two equal-length vectors, in
// float64 precision. A zero-magnitude side scores 0 — defined, not
// NaN — so an all-zero embedding can rank only at the bottom.
func cosine(a, b []float32) float64 {
	var dot, magA, magB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		magA += float64(a[i]) * float64(a[i])
		magB += float64(b[i]) * float64(b[i])
	}
	if magA == 0 || magB == 0 {
		return 0
	}
	return dot / (math.Sqrt(magA) * math.Sqrt(magB))
}

// cloneDocument returns d with a copied metadata map.
func cloneDocument(d Document) Document {
	if d.Metadata == nil {
		return d
	}
	d.Metadata = cloneMetadata(d.Metadata)
	return d
}

func cloneMetadata(metadata map[string]string) map[string]string {
	copied := make(map[string]string, len(metadata))
	for key, value := range metadata {
		copied[key] = value
	}
	return copied
}
