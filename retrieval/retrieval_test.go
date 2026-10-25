package retrieval_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/abubakarsiddik31/golem/embedding"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/retrieval"
	"github.com/abubakarsiddik31/golem/testmodel"
	"github.com/abubakarsiddik31/golem/tool"
)

// axis returns a unit vector along one dimension, so similarity math
// is exact: orthogonal vectors score 0, a vector against itself 1.
func axis(dim, at int) []float32 {
	v := make([]float32, dim)
	v[at] = 1
	return v
}

func TestMemorySearchRanksByCosineSimilarity(t *testing.T) {
	t.Parallel()

	index := retrieval.NewMemory()
	// Three documents along three axes, plus a mixed one.
	err := index.Add(context.Background(), []retrieval.Embedded{
		{Document: retrieval.Document{ID: "pricing", Text: "The plan costs ten dollars.", Metadata: map[string]string{"source": "pricing.md"}}, Embedding: axis(3, 0)},
		{Document: retrieval.Document{ID: "refunds", Text: "Refunds take five days."}, Embedding: axis(3, 1)},
		{Document: retrieval.Document{ID: "shipping", Text: "Shipping is free over fifty."}, Embedding: axis(3, 2)},
		{Document: retrieval.Document{ID: "mixed", Text: "Pricing and refunds together."}, Embedding: []float32{0.6, 0.8, 0}},
	})
	if err != nil {
		t.Fatalf("Add error = %v", err)
	}

	hits, err := index.Search(context.Background(), axis(3, 0), 3)
	if err != nil {
		t.Fatalf("Search error = %v", err)
	}
	if len(hits) != 3 {
		t.Fatalf("Search = %d hits, want k=3", len(hits))
	}
	if hits[0].Document.ID != "pricing" || math.Abs(hits[0].Score-1) > 1e-9 {
		t.Fatalf("best hit = %s (%.4f), want pricing (1)", hits[0].Document.ID, hits[0].Score)
	}
	if hits[1].Document.ID != "mixed" {
		t.Fatalf("second hit = %s, want mixed", hits[1].Document.ID)
	}
	// Orthogonal vectors score 0; the two zeros tie and break by ID.
	if hits[2].Document.ID != "refunds" || hits[2].Score != 0 {
		t.Fatalf("last hit = %s (%.4f), want refunds (0) by ID tie-break", hits[2].Document.ID, hits[2].Score)
	}
	// Metadata rides the hit.
	if hits[0].Document.Metadata["source"] != "pricing.md" {
		t.Fatalf("metadata did not ride the hit: %+v", hits[0].Document)
	}
}

func TestMemoryAddReplacesByID(t *testing.T) {
	t.Parallel()

	index := retrieval.NewMemory()
	ctx := context.Background()
	if err := index.Add(ctx, []retrieval.Embedded{
		{Document: retrieval.Document{ID: "doc", Text: "old text"}, Embedding: axis(2, 0)},
	}); err != nil {
		t.Fatalf("Add error = %v", err)
	}
	if err := index.Add(ctx, []retrieval.Embedded{
		{Document: retrieval.Document{ID: "doc", Text: "new text"}, Embedding: axis(2, 1)},
	}); err != nil {
		t.Fatalf("re-Add error = %v", err)
	}
	if index.Len() != 1 {
		t.Fatalf("Len = %d, want 1 after replacement", index.Len())
	}
	hits, err := index.Search(ctx, axis(2, 1), 5)
	if err != nil {
		t.Fatalf("Search error = %v", err)
	}
	if hits[0].Document.Text != "new text" || math.Abs(hits[0].Score-1) > 1e-9 {
		t.Fatalf("hit = %+v, want the replacement at score 1", hits[0])
	}
}

func TestMemoryDimensionErrorsAreTyped(t *testing.T) {
	t.Parallel()

	index := retrieval.NewMemory()
	if err := index.Add(context.Background(), []retrieval.Embedded{
		{Document: retrieval.Document{ID: "doc"}, Embedding: axis(4, 0)},
	}); err != nil {
		t.Fatalf("Add error = %v", err)
	}

	err := index.Add(context.Background(), []retrieval.Embedded{
		{Document: retrieval.Document{ID: "other"}, Embedding: axis(2, 0)},
	})
	var dim *retrieval.DimensionError
	if !errors.As(err, &dim) || dim.Expected != 4 || dim.Got != 2 || dim.ID != "other" {
		t.Fatalf("Add error = %v, want *DimensionError{4, 2, other}", err)
	}

	_, err = index.Search(context.Background(), axis(2, 0), 1)
	if !errors.As(err, &dim) || dim.Expected != 4 || dim.Got != 2 || dim.ID != "" {
		t.Fatalf("Search error = %v, want *DimensionError{4, 2, empty}", err)
	}
}

func TestMemoryValidatesAndHandlesEdges(t *testing.T) {
	t.Parallel()

	index := retrieval.NewMemory()
	ctx := context.Background()

	if _, err := index.Search(ctx, axis(2, 0), 0); err == nil {
		t.Fatal("Search accepted k=0")
	}
	if err := index.Add(ctx, []retrieval.Embedded{
		{Document: retrieval.Document{ID: "empty"}, Embedding: nil},
	}); err == nil {
		t.Fatal("Add accepted an empty embedding")
	}

	// An empty index has no dimensionality yet: any query answers with
	// zero hits, not an error.
	hits, err := index.Search(ctx, axis(2, 0), 1)
	if err != nil {
		t.Fatalf("Search on an empty index errored: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("Search on an empty index = %v", hits)
	}

	// A k larger than the corpus returns everything.
	if err := index.Add(ctx, []retrieval.Embedded{
		{Document: retrieval.Document{ID: "a"}, Embedding: axis(2, 0)},
		{Document: retrieval.Document{ID: "b"}, Embedding: axis(2, 1)},
	}); err != nil {
		t.Fatalf("Add error = %v", err)
	}
	hits, err = index.Search(ctx, axis(2, 0), 10)
	if err != nil {
		t.Fatalf("Search error = %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("Search = %d hits, want 2", len(hits))
	}

	// A zero vector scores 0, defined rather than NaN.
	if err := index.Add(ctx, []retrieval.Embedded{
		{Document: retrieval.Document{ID: "zero"}, Embedding: make([]float32, 2)},
	}); err != nil {
		t.Fatalf("Add error = %v", err)
	}
	hits, err = index.Search(ctx, axis(2, 0), 10)
	if err != nil {
		t.Fatalf("Search error = %v", err)
	}
	last := hits[len(hits)-1]
	if last.Document.ID != "zero" || last.Score != 0 || math.IsNaN(last.Score) {
		t.Fatalf("zero vector scored %.4f on %s, want 0 on zero", last.Score, last.Document.ID)
	}
}

func TestMemorySnapshotsCallerSlices(t *testing.T) {
	t.Parallel()

	index := retrieval.NewMemory()
	doc := retrieval.Document{ID: "doc", Text: "original", Metadata: map[string]string{"k": "v"}}
	embeddingVec := axis(2, 0)
	if err := index.Add(context.Background(), []retrieval.Embedded{
		{Document: doc, Embedding: embeddingVec},
	}); err != nil {
		t.Fatalf("Add error = %v", err)
	}

	// Mutate everything the caller still holds.
	doc.Text = "mutated"
	doc.Metadata["k"] = "mutated"
	embeddingVec[0] = 0

	hits, err := index.Search(context.Background(), []float32{1, 0}, 5)
	if err != nil {
		t.Fatalf("Search error = %v", err)
	}
	if hits[0].Document.Text != "original" || hits[0].Document.Metadata["k"] != "v" {
		t.Fatalf("store aliased the caller's document: %+v", hits[0].Document)
	}
	if math.Abs(hits[0].Score-1) > 1e-9 {
		t.Fatalf("store aliased the caller's vector: score %.4f", hits[0].Score)
	}
	// Hits carry copied metadata too, so mutating a hit cannot corrupt
	// the index.
	hits[0].Document.Metadata["k"] = "mutated hit"
	again, err := index.Search(context.Background(), []float32{1, 0}, 5)
	if err != nil {
		t.Fatalf("Search error = %v", err)
	}
	if again[0].Document.Metadata["k"] != "v" {
		t.Fatal("a hit's metadata aliased the index")
	}
}

func TestMemoryConcurrentUse(t *testing.T) {
	t.Parallel()

	index := retrieval.NewMemory()
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("doc-%d", i)
			if err := index.Add(ctx, []retrieval.Embedded{
				{Document: retrieval.Document{ID: id}, Embedding: axis(4, i%4)},
			}); err != nil {
				t.Errorf("Add error = %v", err)
				return
			}
			if _, err := index.Search(ctx, axis(4, 0), 3); err != nil {
				t.Errorf("Search error = %v", err)
			}
		}(i)
	}
	wg.Wait()
	if index.Len() != 8 {
		t.Fatalf("Len = %d, want 8", index.Len())
	}
}

// fakeEmbedder embeds each text by hashing its first character onto a
// fixed axis: deterministic, offline, and good enough to prove the
// tool's flow.
func fakeEmbedder(dim int, queries map[string][]float32) embedding.Embedder {
	return testmodel.EmbedFunc(func(_ context.Context, texts []string, query bool) (embedding.Result, error) {
		vectors := make([][]float32, len(texts))
		for i, text := range texts {
			if query {
				if vector, ok := queries[text]; ok {
					vectors[i] = vector
					continue
				}
			}
			vector := make([]float32, dim)
			vector[int(text[0])%dim] = 1
			vectors[i] = vector
		}
		return embedding.Result{Vectors: vectors, Usage: model.Usage{InputTokens: len(texts)}}, nil
	})
}

func TestSearchDocumentToolReturnsTopHits(t *testing.T) {
	t.Parallel()

	index := retrieval.NewMemory()
	err := index.Add(context.Background(), []retrieval.Embedded{
		{Document: retrieval.Document{ID: "pricing", Text: "The plan costs ten dollars."}, Embedding: axis(3, 0)},
		{Document: retrieval.Document{ID: "refunds", Text: "Refunds take five days."}, Embedding: axis(3, 1)},
	})
	if err != nil {
		t.Fatalf("Add error = %v", err)
	}
	// The query embedding comes from the same model that embedded the
	// documents — the tool just drives it.
	embedder := fakeEmbedder(3, map[string][]float32{
		"how much does it cost?": axis(3, 0),
	})

	searchTool := retrieval.MustNew[struct{}](retrieval.ToolConfig{Index: index, Embedder: embedder, K: 1})
	if searchTool.Name != retrieval.ToolName {
		t.Fatalf("tool name = %q", searchTool.Name)
	}
	result, err := searchTool.Exec(context.Background(), struct{}{}, json.RawMessage(`{"query":"how much does it cost?"}`))
	if err != nil {
		t.Fatalf("Exec error = %v", err)
	}
	text := result.Text
	if !strings.Contains(text, "pricing") || !strings.Contains(text, "ten dollars") {
		t.Fatalf("result lost the pricing hit:\n%s", text)
	}
	if strings.Contains(text, "refunds") {
		t.Fatalf("result ranked an orthogonal document above the match:\n%s", text)
	}
}

func TestSearchDocumentToolCorrectsBadArguments(t *testing.T) {
	t.Parallel()

	index := retrieval.NewMemory()
	searchTool := retrieval.MustNew[struct{}](retrieval.ToolConfig{
		Index:    index,
		Embedder: fakeEmbedder(2, nil),
	})

	for name, args := range map[string]string{
		"not an object":  `["query"]`,
		"missing query":  `{}`,
		"empty query":    `{"query": "   "}`,
		"wrong arg type": `{"query": 42}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := searchTool.Exec(context.Background(), struct{}{}, json.RawMessage(args))
			var retry *model.ModelRetry
			if !errors.As(err, &retry) {
				t.Fatalf("Exec error = %v, want a correctable *model.ModelRetry", err)
			}
		})
	}
}

func TestSearchDocumentToolPropagatesEmbeddingFailures(t *testing.T) {
	t.Parallel()

	index := retrieval.NewMemory()
	embedder := testmodel.EmbedFunc(func(ctx context.Context, texts []string, query bool) (embedding.Result, error) {
		return embedding.Result{}, errors.New("quota exhausted")
	})
	searchTool := retrieval.MustNew[struct{}](retrieval.ToolConfig{Index: index, Embedder: embedder})

	_, err := searchTool.Exec(context.Background(), struct{}{}, json.RawMessage(`{"query":"anything"}`))
	if err == nil || !strings.Contains(err.Error(), "quota exhausted") {
		t.Fatalf("Exec error = %v, want the embedding failure", err)
	}
	var retry *model.ModelRetry
	if errors.As(err, &retry) {
		t.Fatal("a provider failure is not a correctable model mistake")
	}
}

func TestSearchDocumentToolEmptyIndex(t *testing.T) {
	t.Parallel()

	searchTool := retrieval.MustNew[struct{}](retrieval.ToolConfig{
		Index:    retrieval.NewMemory(),
		Embedder: fakeEmbedder(2, nil),
	})
	result, err := searchTool.Exec(context.Background(), struct{}{}, json.RawMessage(`{"query":"anything"}`))
	if err != nil {
		t.Fatalf("Exec error = %v", err)
	}
	if !strings.Contains(result.Text, "No documents matched") {
		t.Fatalf("empty search text = %q", result.Text)
	}
}

func TestToolConfigValidation(t *testing.T) {
	t.Parallel()

	embedder := fakeEmbedder(2, nil)
	if _, err := retrieval.New[struct{}](retrieval.ToolConfig{Embedder: embedder}); err == nil {
		t.Error("New accepted a nil index")
	}
	if _, err := retrieval.New[struct{}](retrieval.ToolConfig{Index: retrieval.NewMemory()}); err == nil {
		t.Error("New accepted a nil embedder")
	}
	if _, err := retrieval.New[struct{}](retrieval.ToolConfig{Index: retrieval.NewMemory(), Embedder: embedder, K: -1}); err == nil {
		t.Error("New accepted a negative k")
	}
}

var (
	_ retrieval.Index     = retrieval.NewMemory()
	_ tool.Tool[struct{}] = retrieval.MustNew[struct{}](retrieval.ToolConfig{
		Index:    retrieval.NewMemory(),
		Embedder: fakeEmbedder(2, nil),
	})
)
