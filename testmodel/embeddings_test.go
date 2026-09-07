package testmodel_test

import (
	"context"
	"errors"
	"testing"

	"github.com/abubakarsiddik31/golem/embedding"
	"github.com/abubakarsiddik31/golem/testmodel"
)

func TestEmbedFuncDispatchesQueryAndDocuments(t *testing.T) {
	t.Parallel()

	var calls []struct {
		texts []string
		query bool
	}
	fake := testmodel.EmbedFunc(func(ctx context.Context, texts []string, query bool) (embedding.Result, error) {
		calls = append(calls, struct {
			texts []string
			query bool
		}{texts, query})
		vectors := make([][]float32, 0, len(texts))
		for range texts {
			vectors = append(vectors, []float32{1, 0})
		}
		return embedding.Result{Vectors: vectors}, nil
	})

	queryResult, err := fake.EmbedQuery(context.Background(), "hello")
	if err != nil {
		t.Fatalf("EmbedQuery() error = %v", err)
	}
	if len(queryResult.Vectors) != 1 {
		t.Fatalf("query vectors = %#v, want one", queryResult.Vectors)
	}

	docResult, err := fake.EmbedDocuments(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("EmbedDocuments() error = %v", err)
	}
	if len(docResult.Vectors) != 2 {
		t.Fatalf("document vectors = %#v, want two", docResult.Vectors)
	}

	if len(calls) != 2 {
		t.Fatalf("fake received %d calls, want 2", len(calls))
	}
	if !calls[0].query || len(calls[0].texts) != 1 || calls[0].texts[0] != "hello" {
		t.Fatalf("query call = %#v, want one text, query true", calls[0])
	}
	if calls[1].query || len(calls[1].texts) != 2 {
		t.Fatalf("document call = %#v, want two texts, query false", calls[1])
	}
}

func TestEmbedFuncPropagatesErrors(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("quota exhausted")
	fake := testmodel.EmbedFunc(func(ctx context.Context, texts []string, query bool) (embedding.Result, error) {
		return embedding.Result{}, wantErr
	})
	if _, err := fake.EmbedQuery(context.Background(), "q"); !errors.Is(err, wantErr) {
		t.Fatalf("EmbedQuery() error = %v, want %v", err, wantErr)
	}
	if _, err := fake.EmbedDocuments(context.Background(), []string{"a"}); !errors.Is(err, wantErr) {
		t.Fatalf("EmbedDocuments() error = %v, want %v", err, wantErr)
	}
}
