package azure_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/abubakarsiddik31/golem/embedding"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/azure"
)

var _ embedding.Embedder = (*azure.Embedder)(nil)

func newEmbedder(t *testing.T, endpoint string, dimensions int) *azure.Embedder {
	t.Helper()
	embedder, err := azure.NewEmbedder(azure.EmbedderConfig{
		APIKey:     "test-key",
		Endpoint:   endpoint,
		Deployment: "embed-dep",
		APIVersion: "2024-10-21",
		Dimensions: dimensions,
	})
	if err != nil {
		t.Fatalf("NewEmbedder() error = %v", err)
	}
	return embedder
}

const embedResponseOK = `{
	"data": [
		{"object": "embedding", "embedding": [0.1, 0.2], "index": 0},
		{"object": "embedding", "embedding": [0.3, 0.4], "index": 1}
	],
	"usage": {"prompt_tokens": 12, "total_tokens": 12}
}`

func TestNewEmbedderRejectsMissingConfiguration(t *testing.T) {
	t.Parallel()

	config := func() azure.EmbedderConfig {
		return azure.EmbedderConfig{
			APIKey:     "k",
			Endpoint:   "https://res.openai.azure.com",
			Deployment: "d",
			APIVersion: "2024-10-21",
		}
	}
	missing := config()
	missing.APIKey = ""
	if _, err := azure.NewEmbedder(missing); err == nil {
		t.Fatal("NewEmbedder() error = nil, want missing API key rejection")
	}
	missing = config()
	missing.Endpoint = ""
	if _, err := azure.NewEmbedder(missing); err == nil {
		t.Fatal("NewEmbedder() error = nil, want missing endpoint rejection")
	}
	missing = config()
	missing.Deployment = ""
	if _, err := azure.NewEmbedder(missing); err == nil {
		t.Fatal("NewEmbedder() error = nil, want missing deployment rejection")
	}
	missing = config()
	missing.APIVersion = ""
	if _, err := azure.NewEmbedder(missing); err == nil {
		t.Fatal("NewEmbedder() error = nil, want missing API version rejection")
	}
	if _, err := azure.NewEmbedder(func() azure.EmbedderConfig {
		c := config()
		c.Dimensions = -1
		return c
	}()); err == nil {
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
	if len(result.Vectors) != 2 || result.Vectors[0][1] != 0.2 || result.Vectors[1][0] != 0.3 {
		t.Fatalf("vectors = %#v", result.Vectors)
	}
	if result.Usage != (model.Usage{InputTokens: 12}) {
		t.Fatalf("usage = %#v", result.Usage)
	}

	last := recorder.last(t)
	if last.path != "/openai/deployments/embed-dep/embeddings" {
		t.Fatalf("request path = %q", last.path)
	}
	if last.query != "api-version=2024-10-21" {
		t.Fatalf("request query = %q", last.query)
	}
	if last.apiKey != "test-key" {
		t.Fatalf("api key header = %q", last.apiKey)
	}
	var sent struct {
		Input      []string `json:"input"`
		Model      string   `json:"model"`
		Dimensions int      `json:"dimensions"`
	}
	if err := json.Unmarshal([]byte(last.body), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}
	if len(sent.Input) != 2 || sent.Input[1] != "beta" {
		t.Fatalf("wire input = %#v", sent.Input)
	}
	if sent.Model != "" {
		t.Fatalf("wire model = %q, want unset (the deployment names the model)", sent.Model)
	}
	if sent.Dimensions != 0 {
		t.Fatalf("dimensions rode the request unset = %d", sent.Dimensions)
	}
}

func TestEmbedQuerySendsDimensionsWhenSet(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{"data": [{"embedding": [1], "index": 0}], "usage": {"prompt_tokens": 1}}`)
	defer recorder.server.Close()

	embedder := newEmbedder(t, recorder.server.URL, 1536)
	if _, err := embedder.EmbedQuery(context.Background(), "q"); err != nil {
		t.Fatalf("EmbedQuery() error = %v", err)
	}
	var sent struct {
		Dimensions int `json:"dimensions"`
	}
	if err := json.Unmarshal([]byte(recorder.last(t).body), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}
	if sent.Dimensions != 1536 {
		t.Fatalf("wire dimensions = %d, want 1536", sent.Dimensions)
	}
}

func TestEmbedderValidatesInputBeforeNetwork(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, embedResponseOK)
	defer recorder.server.Close()

	embedder := newEmbedder(t, recorder.server.URL, 0)
	if _, err := embedder.EmbedDocuments(context.Background(), nil); err == nil {
		t.Fatal("EmbedDocuments() error = nil, want input rejection")
	} else {
		var decodeErr *azure.DecodeError
		if !errors.As(err, &decodeErr) {
			t.Fatalf("error = %v, want *azure.DecodeError", err)
		}
	}
	if _, err := embedder.EmbedQuery(context.Background(), ""); err == nil {
		t.Fatal("EmbedQuery() error = nil, want input rejection")
	}
	if got := recorder.requestCount(); got != 0 {
		t.Fatalf("server received %d requests, want 0", got)
	}
}

func TestEmbedderClassifiesProviderErrors(t *testing.T) {
	t.Parallel()

	rateLimited := newRecordedServer(http.StatusTooManyRequests, `{"error": {"message": "slow down"}}`)
	defer rateLimited.server.Close()
	embedder := newEmbedder(t, rateLimited.server.URL, 0)
	_, err := embedder.EmbedQuery(context.Background(), "q")
	var apiErr *azure.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *azure.APIError", err)
	}
	if apiErr.StatusCode != http.StatusTooManyRequests || !apiErr.Retryable() {
		t.Fatalf("APIError = %+v, want retryable 429", apiErr)
	}

	notFound := newRecordedServer(http.StatusNotFound, `{"error": {"message": "no such deployment"}}`)
	defer notFound.server.Close()
	embedder = newEmbedder(t, notFound.server.URL, 0)
	_, err = embedder.EmbedQuery(context.Background(), "q")
	if !errors.As(err, &apiErr) || apiErr.Retryable() {
		t.Fatalf("error = %v, want non-retryable *azure.APIError", err)
	}
}

func TestEmbedderRejectsMalformedResponses(t *testing.T) {
	t.Parallel()

	malformed := newRecordedServer(http.StatusOK, `{"data": `)
	defer malformed.server.Close()
	embedder := newEmbedder(t, malformed.server.URL, 0)
	_, err := embedder.EmbedQuery(context.Background(), "q")
	var decodeErr *azure.DecodeError
	if !errors.As(err, &decodeErr) {
		t.Fatalf("error = %v, want *azure.DecodeError", err)
	}

	countMismatch := newRecordedServer(http.StatusOK, `{"data": [{"embedding": [1], "index": 0}]}`)
	defer countMismatch.server.Close()
	embedder = newEmbedder(t, countMismatch.server.URL, 0)
	_, err = embedder.EmbedDocuments(context.Background(), []string{"a", "b"})
	if !errors.As(err, &decodeErr) {
		t.Fatalf("error = %v, want *azure.DecodeError", err)
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
	var transportErr *azure.TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("error = %v, want *azure.TransportError", err)
	}
	if transportErr.Retryable() {
		t.Fatal("canceled request reported retryable, want false")
	}
}
