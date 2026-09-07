package testmodel

import (
	"context"

	"github.com/abubakarsiddik31/golem/embedding"
)

// EmbedFunc adapts a function to the embedding.Embedder port: the
// function receives the exact texts the caller passed and which side of
// the query/documents split was invoked, and decides the result or
// error. query is true for EmbedQuery — whose texts always hold exactly
// the one query — and false for EmbedDocuments.
type EmbedFunc func(ctx context.Context, texts []string, query bool) (embedding.Result, error)

var _ embedding.Embedder = EmbedFunc(nil)

// EmbedDocuments delegates to f with query false.
func (f EmbedFunc) EmbedDocuments(ctx context.Context, texts []string) (embedding.Result, error) {
	return f(ctx, texts, false)
}

// EmbedQuery delegates to f with query true and the single text.
func (f EmbedFunc) EmbedQuery(ctx context.Context, text string) (embedding.Result, error) {
	return f(ctx, []string{text}, true)
}
