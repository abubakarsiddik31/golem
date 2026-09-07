// Command embeddings runs a tiny semantic search: three documents are
// embedded in one call, a query is embedded on the query side of the
// split, and cosine similarity ranks the documents — the retrieval half
// of a RAG pipeline, with the vector store left as an exercise.
//
// Set OPENAI_API_KEY (and optionally OPENAI_EMBED_MODEL) to run it.
// Any OpenAI-compatible embeddings endpoint works; point OPENAI_BASE
// at a local runtime such as Ollama (http://localhost:11434/v1) to run
// without an OpenAI key, e.g. with OPENAI_EMBED_MODEL=nomic-embed-text.
package main

import (
	"context"
	"fmt"
	"math"
	"os"

	"github.com/abubakarsiddik31/golem/providers/openai"
)

var documents = []string{
	"Golem agents keep run history as durable message evidence.",
	"Gemini embeddings condition queries and documents differently.",
	"Cosine similarity compares vector directions, ignoring length.",
}

func main() {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" && os.Getenv("OPENAI_BASE") == "" {
		fmt.Println("Set OPENAI_API_KEY (and optionally OPENAI_EMBED_MODEL, OPENAI_BASE) to run this example.")
		return
	}
	modelName := os.Getenv("OPENAI_EMBED_MODEL")
	if modelName == "" {
		modelName = "text-embedding-3-small"
	}

	embedder, err := openai.NewEmbedder(openai.EmbedderConfig{
		APIKey:  apiKey,
		BaseURL: os.Getenv("OPENAI_BASE"),
		Model:   modelName,
	})
	if err != nil {
		fmt.Println("openai.NewEmbedder:", err)
		return
	}

	ctx := context.Background()
	docResult, err := embedder.EmbedDocuments(ctx, documents)
	if err != nil {
		fmt.Println("EmbedDocuments:", err)
		return
	}
	fmt.Printf("embedded %d documents, %d vectors of width %d (%d input tokens)\n",
		len(documents), len(docResult.Vectors), len(docResult.Vectors[0]), docResult.Usage.InputTokens)

	query := os.Getenv("OPENAI_QUERY")
	if query == "" {
		query = "how do I search my notes by meaning?"
	}
	queryResult, err := embedder.EmbedQuery(ctx, query)
	if err != nil {
		fmt.Println("EmbedQuery:", err)
		return
	}

	fmt.Printf("query %q ranks:\n", query)
	scores := rank(queryResult.Vectors[0], docResult.Vectors)
	for _, s := range scores {
		fmt.Printf("  %.4f  %s\n", s.score, documents[s.index])
	}
}

type score struct {
	index int
	score float64
}

// rank scores each document vector against the query by cosine
// similarity and returns them best-first.
func rank(query []float32, documents [][]float32) []score {
	scores := make([]score, 0, len(documents))
	for i, document := range documents {
		scores = append(scores, score{index: i, score: cosine(query, document)})
	}
	for i := 1; i < len(scores); i++ {
		for j := i; j > 0 && scores[j].score > scores[j-1].score; j-- {
			scores[j], scores[j-1] = scores[j-1], scores[j]
		}
	}
	return scores
}

func cosine(a, b []float32) float64 {
	var dot, magnitudeA, magnitudeB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		magnitudeA += float64(a[i]) * float64(a[i])
		magnitudeB += float64(b[i]) * float64(b[i])
	}
	if magnitudeA == 0 || magnitudeB == 0 {
		return 0
	}
	return dot / (math.Sqrt(magnitudeA) * math.Sqrt(magnitudeB))
}
