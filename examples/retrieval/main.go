// Command retrieval builds a searchable knowledge base over the
// retrieval port and hands it to an agent as a tool: documents are
// embedded and indexed here, the model searches when it needs a fact.
// The embedder is a deterministic stand-in, so the example runs
// offline — swap it for a real embedding provider in production.
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/embedding"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/retrieval"
	"github.com/abubakarsiddik31/golem/testmodel"
)

// keywordEmbedder is a stand-in embedding provider: each text becomes
// a vector counting a few domain keywords. Real quality needs a real
// Embedder — the interface it implements is the seam.
type keywordEmbedder struct {
	words []string
}

func (e *keywordEmbedder) embed(text string) []float32 {
	vector := make([]float32, len(e.words))
	for i, word := range e.words {
		if containsFold(text, word) {
			vector[i] = 1
		}
	}
	return vector
}

func (e *keywordEmbedder) EmbedDocuments(_ context.Context, texts []string) (embedding.Result, error) {
	vectors := make([][]float32, len(texts))
	for i, text := range texts {
		vectors[i] = e.embed(text)
	}
	return embedding.Result{Vectors: vectors}, nil
}

func (e *keywordEmbedder) EmbedQuery(ctx context.Context, text string) (embedding.Result, error) {
	result, err := e.EmbedDocuments(ctx, []string{text})
	if err != nil {
		return embedding.Result{}, err
	}
	return embedding.Result{Vectors: result.Vectors[:1]}, nil
}

func containsFold(haystack, needle string) bool {
	n, h := len(needle), len(haystack)
	for i := 0; i+n <= h; i++ {
		if equalFold(haystack[i:i+n], needle) {
			return true
		}
	}
	return false
}

func equalFold(a, b string) bool {
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

func main() {
	embedder := &keywordEmbedder{words: []string{"price", "refund", "ship", "warranty", "account"}}

	// Build the index: chunk, embed, add. Chunking is the
	// application's job — here each policy section is one chunk.
	sections := []retrieval.Document{
		{ID: "pricing#plans", Text: "The Starter plan price is nine dollars per month. The Team plan price is twenty-nine dollars per month."},
		{ID: "refunds#window", Text: "A refund is available within thirty days. A refund is issued to the original payment method."},
		{ID: "shipping#times", Text: "Physical orders ship in two business days. Shipping is free over fifty dollars."},
		{ID: "warranty#terms", Text: "Hardware carries a one year warranty. The warranty does not cover water damage."},
	}
	embeddings, err := embedder.EmbedDocuments(context.Background(), textsOf(sections))
	if err != nil {
		fmt.Println("EmbedDocuments:", err)
		return
	}
	index := retrieval.NewMemory()
	embedded := make([]retrieval.Embedded, len(sections))
	for i, doc := range sections {
		embedded[i] = retrieval.Embedded{Document: doc, Embedding: embeddings.Vectors[i]}
	}
	if err := index.Add(context.Background(), embedded); err != nil {
		fmt.Println("Add:", err)
		return
	}

	// Direct search: what the application sees before any agent runs.
	hits, err := index.Search(context.Background(), embedder.embed("price refund"), 2)
	if err != nil {
		fmt.Println("Search:", err)
		return
	}
	fmt.Println("top matches for \"price refund\":")
	for _, hit := range hits {
		fmt.Printf("  [%s] score %.2f\n", hit.Document.ID, hit.Score)
	}

	// The same index, as the agent's tool: the model searches when a
	// question needs a fact.
	answer := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: retrieval.ToolName, Args: json.RawMessage(`{"query":"how much is the team plan?"}`)},
		}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "The Team plan is twenty-nine dollars per month."}},
	)
	agent, err := golem.New[struct{}, string](answer,
		golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) {
			return r.Message.Content, nil
		}),
		golem.WithTools[struct{}, string](
			retrieval.MustNew[struct{}](retrieval.ToolConfig{Index: index, Embedder: embedder}),
		),
	)
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}
	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{},
		"Support question: how much is the team plan?")
	if err != nil {
		fmt.Println("Run:", err)
		return
	}
	fmt.Println("agent:", result.Output)
}

func textsOf(docs []retrieval.Document) []string {
	texts := make([]string, len(docs))
	for i, doc := range docs {
		texts[i] = doc.Text
	}
	return texts
}
