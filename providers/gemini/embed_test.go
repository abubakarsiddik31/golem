package gemini_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/abubakarsiddik31/golem/embedding"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/gemini"
)

var _ embedding.Embedder = (*gemini.Embedder)(nil)

func newEmbedder(t *testing.T, baseURL string, dimensions int) *gemini.Embedder {
	t.Helper()
	embedder, err := gemini.NewEmbedder(gemini.EmbedderConfig{
		APIKey:     "test-key",
		BaseURL:    baseURL,
		Model:      "gemini-embedding-001",
		Dimensions: dimensions,
	})
	if err != nil {
		t.Fatalf("NewEmbedder() error = %v", err)
	}
	return embedder
}

func TestNewEmbedderRejectsMissingConfiguration(t *testing.T) {
	t.Parallel()

	if _, err := gemini.NewEmbedder(gemini.EmbedderConfig{Model: "m"}); err == nil {
		t.Fatal("NewEmbedder() error = nil, want missing API key rejection")
	}
	if _, err := gemini.NewEmbedder(gemini.EmbedderConfig{APIKey: "k"}); err == nil {
		t.Fatal("NewEmbedder() error = nil, want missing model rejection")
	}
	if _, err := gemini.NewEmbedder(gemini.EmbedderConfig{APIKey: "k", Model: "m", Dimensions: -1}); err == nil {
		t.Fatal("NewEmbedder() error = nil, want negative dimensions rejection")
	}
}

func TestEmbedQueryUsesEmbedContentWithRetrievalQuery(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{
		"embedding": {"values": [0.5, 0.25]},
		"usageMetadata": {"promptTokenCount": 7}
	}`)
	defer recorder.server.Close()

	embedder := newEmbedder(t, recorder.server.URL, 0)
	result, err := embedder.EmbedQuery(context.Background(), "what is go?")
	if err != nil {
		t.Fatalf("EmbedQuery() error = %v", err)
	}
	if len(result.Vectors) != 1 || len(result.Vectors[0]) != 2 || result.Vectors[0][1] != 0.25 {
		t.Fatalf("vectors = %#v", result.Vectors)
	}
	if result.Usage != (model.Usage{InputTokens: 7}) {
		t.Fatalf("usage = %#v", result.Usage)
	}

	last := recorder.last(t)
	if last.path != "/v1beta/models/gemini-embedding-001:embedContent" {
		t.Fatalf("request path = %q", last.path)
	}
	if last.apiKey != "test-key" {
		t.Fatalf("api key header = %q", last.apiKey)
	}
	var sent struct {
		Model   string `json:"model"`
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
		TaskType             string `json:"taskType"`
		OutputDimensionality int    `json:"outputDimensionality"`
	}
	if err := json.Unmarshal([]byte(last.body), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}
	if sent.Model != "models/gemini-embedding-001" {
		t.Fatalf("wire model = %q", sent.Model)
	}
	if len(sent.Content.Parts) != 1 || sent.Content.Parts[0].Text != "what is go?" {
		t.Fatalf("wire content = %#v", sent.Content)
	}
	if sent.TaskType != "RETRIEVAL_QUERY" {
		t.Fatalf("wire task type = %q, want RETRIEVAL_QUERY", sent.TaskType)
	}
	if sent.OutputDimensionality != 0 {
		t.Fatalf("outputDimensionality rode the request unset = %d", sent.OutputDimensionality)
	}
}

func TestEmbedDocumentsUsesBatchEndpointWithRetrievalDocument(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{
		"embeddings": [
			{"values": [1, 2]},
			{"values": [3, 4]}
		],
		"usageMetadata": {"promptTokenCount": 20}
	}`)
	defer recorder.server.Close()

	embedder := newEmbedder(t, recorder.server.URL, 0)
	result, err := embedder.EmbedDocuments(context.Background(), []string{"alpha", "beta"})
	if err != nil {
		t.Fatalf("EmbedDocuments() error = %v", err)
	}
	if len(result.Vectors) != 2 || result.Vectors[0][0] != 1 || result.Vectors[1][1] != 4 {
		t.Fatalf("vectors = %#v, want input order preserved", result.Vectors)
	}
	if result.Usage != (model.Usage{InputTokens: 20}) {
		t.Fatalf("usage = %#v", result.Usage)
	}

	last := recorder.last(t)
	if last.path != "/v1beta/models/gemini-embedding-001:batchEmbedContents" {
		t.Fatalf("request path = %q", last.path)
	}
	var sent struct {
		Requests []struct {
			Model   string `json:"model"`
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
			TaskType             string `json:"taskType"`
			OutputDimensionality int    `json:"outputDimensionality"`
		} `json:"requests"`
	}
	if err := json.Unmarshal([]byte(last.body), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}
	if len(sent.Requests) != 2 {
		t.Fatalf("wire requests = %#v, want two", sent.Requests)
	}
	if sent.Requests[0].Model != "models/gemini-embedding-001" || sent.Requests[1].Model != "models/gemini-embedding-001" {
		t.Fatalf("per-request models = %q, %q", sent.Requests[0].Model, sent.Requests[1].Model)
	}
	if sent.Requests[0].Content.Parts[0].Text != "alpha" || sent.Requests[1].Content.Parts[0].Text != "beta" {
		t.Fatalf("wire texts = %#v", sent.Requests)
	}
	for i, request := range sent.Requests {
		if request.TaskType != "RETRIEVAL_DOCUMENT" {
			t.Fatalf("request %d task type = %q, want RETRIEVAL_DOCUMENT", i, request.TaskType)
		}
	}
}

func TestEmbedderSendsDimensionsWhenSet(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{
		"embedding": {"values": [1]},
		"usageMetadata": {"promptTokenCount": 1}
	}`)
	defer recorder.server.Close()

	embedder := newEmbedder(t, recorder.server.URL, 768)
	if _, err := embedder.EmbedQuery(context.Background(), "q"); err != nil {
		t.Fatalf("EmbedQuery() error = %v", err)
	}
	var sent struct {
		OutputDimensionality int `json:"outputDimensionality"`
	}
	if err := json.Unmarshal([]byte(recorder.last(t).body), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}
	if sent.OutputDimensionality != 768 {
		t.Fatalf("wire outputDimensionality = %d, want 768", sent.OutputDimensionality)
	}
}

func TestEmbedderValidatesInputBeforeNetwork(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{"embeddings": [{"values": [1]}]}`)
	defer recorder.server.Close()

	embedder := newEmbedder(t, recorder.server.URL, 0)
	for name, texts := range map[string][]string{
		"empty batch":  nil,
		"empty string": {"alpha", ""},
	} {
		if _, err := embedder.EmbedDocuments(context.Background(), texts); err == nil {
			t.Fatalf("%s: EmbedDocuments() error = nil, want input rejection", name)
		} else {
			var decodeErr *gemini.DecodeError
			if !errors.As(err, &decodeErr) {
				t.Fatalf("%s: error = %v, want *gemini.DecodeError", name, err)
			}
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

	rateLimited := newRecordedServer(http.StatusTooManyRequests, `{"error": {"code": 429, "message": "quota exceeded", "status": "RESOURCE_EXHAUSTED"}}`)
	defer rateLimited.server.Close()
	embedder := newEmbedder(t, rateLimited.server.URL, 0)
	_, err := embedder.EmbedQuery(context.Background(), "q")
	var apiErr *gemini.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *gemini.APIError", err)
	}
	if apiErr.StatusCode != http.StatusTooManyRequests || !apiErr.Retryable() {
		t.Fatalf("APIError = %+v, want retryable 429", apiErr)
	}

	badRequest := newRecordedServer(http.StatusBadRequest, `{"error": {"code": 400, "message": "bad task type", "status": "INVALID_ARGUMENT"}}`)
	defer badRequest.server.Close()
	embedder = newEmbedder(t, badRequest.server.URL, 0)
	_, err = embedder.EmbedQuery(context.Background(), "q")
	if !errors.As(err, &apiErr) || apiErr.Retryable() {
		t.Fatalf("error = %v, want non-retryable *gemini.APIError", err)
	}
}

func TestEmbedderRejectsMalformedResponses(t *testing.T) {
	t.Parallel()

	malformed := newRecordedServer(http.StatusOK, `{"embedding": `)
	defer malformed.server.Close()
	embedder := newEmbedder(t, malformed.server.URL, 0)
	_, err := embedder.EmbedQuery(context.Background(), "q")
	var decodeErr *gemini.DecodeError
	if !errors.As(err, &decodeErr) {
		t.Fatalf("error = %v, want *gemini.DecodeError", err)
	}

	emptyVector := newRecordedServer(http.StatusOK, `{"embedding": {"values": []}, "usageMetadata": {"promptTokenCount": 1}}`)
	defer emptyVector.server.Close()
	embedder = newEmbedder(t, emptyVector.server.URL, 0)
	_, err = embedder.EmbedQuery(context.Background(), "q")
	if !errors.As(err, &decodeErr) {
		t.Fatalf("error = %v, want *gemini.DecodeError", err)
	}

	countMismatch := newRecordedServer(http.StatusOK, `{"embeddings": [{"values": [1]}], "usageMetadata": {"promptTokenCount": 1}}`)
	defer countMismatch.server.Close()
	embedder = newEmbedder(t, countMismatch.server.URL, 0)
	_, err = embedder.EmbedDocuments(context.Background(), []string{"a", "b"})
	if !errors.As(err, &decodeErr) {
		t.Fatalf("error = %v, want *gemini.DecodeError", err)
	}
}

func TestEmbedderCancellationReturnsTransportError(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{"embedding": {"values": [1]}}`)
	defer recorder.server.Close()

	embedder := newEmbedder(t, recorder.server.URL, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := embedder.EmbedQuery(ctx, "q")
	var transportErr *gemini.TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("error = %v, want *gemini.TransportError", err)
	}
	if transportErr.Retryable() {
		t.Fatal("canceled request reported retryable, want false")
	}
}
