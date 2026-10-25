package retrieval

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/abubakarsiddik31/golem/embedding"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

// ToolName is the search tool's name.
const ToolName = "search_documents"

// DefaultK is the number of hits the tool returns when Config.K is
// unset.
const DefaultK = 5

// ToolDescription tells the model when to call the search tool.
const ToolDescription = "Search the knowledge base for passages relevant to a query. " +
	"Returns the most similar stored documents, best first, with their similarity scores. " +
	"Use it for facts that are not in the conversation and that you were told this index covers."

const toolSchema = `{
	"type": "object",
	"properties": {
		"query": {
			"type": "string",
			"description": "What to search for, phrased as a search query."
		}
	},
	"required": ["query"],
	"additionalProperties": false
}`

// Config configures the search tool.
type ToolConfig struct {
	// Index is the knowledge base to search; required.
	Index Index
	// Embedder converts the model's query to a vector; required. Use
	// the same embedding model that produced the stored vectors —
	// scores across different models are meaningless.
	Embedder embedding.Embedder
	// K caps the hits per search. Zero selects DefaultK; negative
	// values fail New.
	K int
}

// searcher holds the resolved configuration behind the tool's exec.
type searcher struct {
	index    Index
	embedder embedding.Embedder
	k        int
}

// New validates cfg and returns the search tool ready for
// registration with an agent. Deps is the agent's dependency type; the
// search itself does not use it, but the tool carries it so one
// constructor serves every agent.
func New[Deps any](cfg ToolConfig) (tool.Tool[Deps], error) {
	if cfg.Index == nil {
		return tool.Tool[Deps]{}, fmt.Errorf("retrieval: index is required")
	}
	if cfg.Embedder == nil {
		return tool.Tool[Deps]{}, fmt.Errorf("retrieval: embedder is required")
	}
	if cfg.K < 0 {
		return tool.Tool[Deps]{}, fmt.Errorf("retrieval: k must not be negative, got %d", cfg.K)
	}
	k := cfg.K
	if k == 0 {
		k = DefaultK
	}
	s := &searcher{index: cfg.Index, embedder: cfg.Embedder, k: k}
	return tool.New(tool.Tool[Deps]{
		Name:        ToolName,
		Description: ToolDescription,
		Schema:      json.RawMessage(toolSchema),
		Exec: func(ctx context.Context, deps Deps, args json.RawMessage) (tool.Result, error) {
			text, err := s.search(ctx, args)
			return tool.Text(text), err
		},
	})
}

// MustNew is New for tools declared as package-level values; it panics
// on invalid configuration.
func MustNew[Deps any](cfg ToolConfig) tool.Tool[Deps] {
	validated, err := New[Deps](cfg)
	if err != nil {
		panic(err)
	}
	return validated
}

// search runs one search_documents call: validate the model-produced
// query as correctable arguments, embed it, and render the top hits.
func (s *searcher) search(ctx context.Context, args json.RawMessage) (string, error) {
	var request struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(args, &request); err != nil {
		return "", &model.ModelRetry{Err: fmt.Errorf("arguments must be an object with a string query: %w", err)}
	}
	query := strings.TrimSpace(request.Query)
	if query == "" {
		return "", &model.ModelRetry{Err: fmt.Errorf("search_documents: query must not be empty")}
	}

	embedded, err := s.embedder.EmbedQuery(ctx, query)
	if err != nil {
		return "", fmt.Errorf("retrieval: embed query: %w", err)
	}
	if len(embedded.Vectors) != 1 {
		return "", fmt.Errorf("retrieval: embed query returned %d vectors, want 1", len(embedded.Vectors))
	}
	hits, err := s.index.Search(ctx, embedded.Vectors[0], s.k)
	if err != nil {
		return "", fmt.Errorf("retrieval: search: %w", err)
	}
	if len(hits) == 0 {
		return "No documents matched the query.", nil
	}

	var out strings.Builder
	for i, hit := range hits {
		fmt.Fprintf(&out, "%d. [%s] score %.4f\n", i+1, hit.Document.ID, hit.Score)
		if hit.Document.Text != "" {
			out.WriteString(hit.Document.Text)
			out.WriteString("\n")
		}
		if len(hit.Document.Metadata) > 0 {
			keys := make([]string, 0, len(hit.Document.Metadata))
			for key := range hit.Document.Metadata {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				fmt.Fprintf(&out, "%s: %s\n", key, hit.Document.Metadata[key])
			}
		}
		if i < len(hits)-1 {
			out.WriteString("\n")
		}
	}
	return out.String(), nil
}
