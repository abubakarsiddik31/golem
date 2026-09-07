package openai_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/abubakarsiddik31/golem/embedding"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/openai"
)

var _ embedding.Embedder = (*openai.Embedder)(nil)

func newEmbedder(t *testing.T, baseURL string, dimensions int) *openai.Embedder {
	t.Helper()
	embedder, err := openai.NewEmbedder(openai.EmbedderConfig{
		APIKey:     "test-key",
		BaseURL:    baseURL,
		Model:      "text-embedding-3-small",
		Dimensions: dimensions,
	})
	if err != nil {
		t.Fatalf("NewEmbedder() error = %v", err)
	}
	return embedder
}

const embedResponseOK = `{
	"object": "list",
	"data": [
		{"object": "embedding", "embedding": [0.1, 0.2], "index": 0},
		{"object": "embedding", "embedding": [0.3, 0.4], "index": 1}
	],
	"model": "text-embedding-3-small",
	"usage": {"prompt_tokens": 12, "total_tokens": 12}
}`

func TestNewEmbedderRejectsMissingConfiguration(t *testing.T) {
	t.Parallel()

	if _, err := openai.NewEmbedder(openai.EmbedderConfig{Model: "m"}); err == nil {
		t.Fatal("NewEmbedder() error = nil, want missing API key rejection")
	}
	if _, err := openai.NewEmbedder(openai.EmbedderConfig{APIKey: "k"}); err == nil {
		t.Fatal("NewEmbedder() error = nil, want missing model rejection")
	}
	if _, err := openai.NewEmbedder(openai.EmbedderConfig{APIKey: "k", Model: "m", Dimensions: -1}); err == nil {
		t.Fatal("NewEmbedder() error = nil, want negative dimensions rejection")
	}
}

func TestEmbedDocumentsTranslatesRequestAndResponse(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, embedResponseOK)
	defer recorder.server.Close()

	embedder := newEmbedder(t, recorder.server.URL, 0)
	result, err := embedder.EmbedDocuments(context.Background(), []string{"alpha", "beta"})
	if err != nil {
		t.Fatalf("EmbedDocuments() error = %v", err)
	}
	want := embedding.Result{
		Vectors: [][]float32{{0.1, 0.2}, {0.3, 0.4}},
		Usage:   model.Usage{InputTokens: 12},
	}
	if len(result.Vectors) != 2 || result.Vectors[0][0] != 0.1 || result.Vectors[1][1] != 0.4 {
		t.Fatalf("vectors = %#v", result.Vectors)
	}
	if result.Usage != want.Usage {
		t.Fatalf("usage = %#v, want %#v", result.Usage, want.Usage)
	}

	if path := recorder.lastPath(t); path != "/embeddings" {
		t.Fatalf("request path = %q, want /embeddings", path)
	}
	if auth := recorder.lastAuth(t); auth != "Bearer test-key" {
		t.Fatalf("authorization = %q", auth)
	}
	var sent struct {
		Model      string   `json:"model"`
		Input      []string `json:"input"`
		Dimensions int      `json:"dimensions"`
	}
	if err := json.Unmarshal([]byte(recorder.lastBody(t)), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}
	if sent.Model != "text-embedding-3-small" {
		t.Fatalf("wire model = %q", sent.Model)
	}
	if len(sent.Input) != 2 || sent.Input[0] != "alpha" || sent.Input[1] != "beta" {
		t.Fatalf("wire input = %#v", sent.Input)
	}
	if sent.Dimensions != 0 {
		t.Fatalf("dimensions rode the request unset = %d", sent.Dimensions)
	}
}

func TestEmbedQuerySendsSingleInput(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{
		"data": [{"embedding": [1, 2, 3], "index": 0}],
		"usage": {"prompt_tokens": 4, "total_tokens": 4}
	}`)
	defer recorder.server.Close()

	embedder := newEmbedder(t, recorder.server.URL, 0)
	result, err := embedder.EmbedQuery(context.Background(), "what is go?")
	if err != nil {
		t.Fatalf("EmbedQuery() error = %v", err)
	}
	if len(result.Vectors) != 1 || len(result.Vectors[0]) != 3 {
		t.Fatalf("vectors = %#v, want one 3-wide vector", result.Vectors)
	}
	var sent struct {
		Input []string `json:"input"`
	}
	if err := json.Unmarshal([]byte(recorder.lastBody(t)), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}
	if len(sent.Input) != 1 || sent.Input[0] != "what is go?" {
		t.Fatalf("wire input = %#v", sent.Input)
	}
}

func TestEmbedderSendsDimensionsWhenSet(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{
		"data": [{"embedding": [1], "index": 0}],
		"usage": {"prompt_tokens": 1, "total_tokens": 1}
	}`)
	defer recorder.server.Close()

	embedder := newEmbedder(t, recorder.server.URL, 256)
	if _, err := embedder.EmbedQuery(context.Background(), "q"); err != nil {
		t.Fatalf("EmbedQuery() error = %v", err)
	}
	var sent struct {
		Dimensions int `json:"dimensions"`
	}
	if err := json.Unmarshal([]byte(recorder.lastBody(t)), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}
	if sent.Dimensions != 256 {
		t.Fatalf("wire dimensions = %d, want 256", sent.Dimensions)
	}
}

func TestEmbedderValidatesInputBeforeNetwork(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, embedResponseOK)
	defer recorder.server.Close()

	embedder := newEmbedder(t, recorder.server.URL, 0)
	for name, texts := range map[string][]string{
		"empty batch":   nil,
		"empty string":  {"alpha", ""},
		"blank strings": {"", "beta"},
	} {
		if _, err := embedder.EmbedDocuments(context.Background(), texts); err == nil {
			t.Fatalf("%s: EmbedDocuments() error = nil, want input rejection", name)
		} else {
			var decodeErr *openai.DecodeError
			if !errors.As(err, &decodeErr) {
				t.Fatalf("%s: error = %v, want *openai.DecodeError", name, err)
			}
		}
		if _, err := embedder.EmbedQuery(context.Background(), ""); err == nil {
			t.Fatalf("%s: EmbedQuery() error = nil, want input rejection", name)
		}
	}
	if got := recorder.requestCount(); got != 0 {
		t.Fatalf("server received %d requests, want 0", got)
	}
}

func TestEmbedderClassifiesProviderErrors(t *testing.T) {
	t.Parallel()

	rateLimited := newRecordedServer(http.StatusTooManyRequests, `{"error": {"message": "slow down", "code": "rate_limit_exceeded"}}`)
	defer rateLimited.server.Close()
	embedder := newEmbedder(t, rateLimited.server.URL, 0)
	_, err := embedder.EmbedQuery(context.Background(), "q")
	var apiErr *openai.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *openai.APIError", err)
	}
	if apiErr.StatusCode != http.StatusTooManyRequests || !apiErr.Retryable() {
		t.Fatalf("APIError = %+v, want retryable 429", apiErr)
	}

	badRequest := newRecordedServer(http.StatusBadRequest, `{"error": {"message": "bad input"}}`)
	defer badRequest.server.Close()
	embedder = newEmbedder(t, badRequest.server.URL, 0)
	_, err = embedder.EmbedQuery(context.Background(), "q")
	if !errors.As(err, &apiErr) || apiErr.Retryable() {
		t.Fatalf("error = %v, want non-retryable *openai.APIError", err)
	}
}

func TestEmbedderRejectsMalformedResponses(t *testing.T) {
	t.Parallel()

	malformed := newRecordedServer(http.StatusOK, `{"data": [`)
	defer malformed.server.Close()
	embedder := newEmbedder(t, malformed.server.URL, 0)
	_, err := embedder.EmbedQuery(context.Background(), "q")
	var decodeErr *openai.DecodeError
	if !errors.As(err, &decodeErr) {
		t.Fatalf("error = %v, want *openai.DecodeError", err)
	}

	countMismatch := newRecordedServer(http.StatusOK, `{"data": [{"embedding": [1], "index": 0}], "usage": {"prompt_tokens": 1}}`)
	defer countMismatch.server.Close()
	embedder = newEmbedder(t, countMismatch.server.URL, 0)
	_, err = embedder.EmbedDocuments(context.Background(), []string{"a", "b"})
	if !errors.As(err, &decodeErr) {
		t.Fatalf("error = %v, want *openai.DecodeError", err)
	}

	emptyVector := newRecordedServer(http.StatusOK, `{"data": [{"embedding": [], "index": 0}], "usage": {"prompt_tokens": 1}}`)
	defer emptyVector.server.Close()
	embedder = newEmbedder(t, emptyVector.server.URL, 0)
	_, err = embedder.EmbedQuery(context.Background(), "q")
	if !errors.As(err, &decodeErr) {
		t.Fatalf("error = %v, want *openai.DecodeError", err)
	}
}

func TestEmbedderCancellationReturnsTransportError(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, embedResponseOK)
	defer recorder.server.Close()

	embedder := newEmbedder(t, recorder.server.URL, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := embedder.EmbedQuery(ctx, "q")
	var transportErr *openai.TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("error = %v, want *openai.TransportError", err)
	}
	if transportErr.Retryable() {
		t.Fatal("canceled request reported retryable, want false")
	}
}
