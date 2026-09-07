package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/abubakarsiddik31/golem/embedding"
	"github.com/abubakarsiddik31/golem/model"
)

// The query/documents split is Gemini's retrieval task type: the two
// sides of a retrieval pair embed differently, and this translation is
// contract (pinned by tests).
const (
	embedTaskQuery    = "RETRIEVAL_QUERY"
	embedTaskDocument = "RETRIEVAL_DOCUMENT"
)

// EmbedderConfig configures an Embedder. APIKey and Model are required;
// the zero values of the remaining fields select documented defaults.
// There are no implicit environment reads: callers wire os.Getenv
// themselves.
type EmbedderConfig struct {
	// APIKey authenticates requests via the x-goog-api-key header.
	APIKey string
	// BaseURL prefixes the model path; defaults to DefaultBaseURL. Point
	// it at an API-compatible proxy when needed.
	BaseURL string
	// Model names the embedding model, e.g. "gemini-embedding-001".
	Model string
	// Dimensions truncates the output vectors to the given width
	// (outputDimensionality). Newer models accept it; zero omits the
	// field and lets the provider default apply; negative values fail
	// NewEmbedder.
	Dimensions int
	// HTTPClient performs requests; defaults to a client with a 5-minute
	// timeout. Callers wanting different timeout behavior supply their
	// own; cancellation always flows through ctx.
	HTTPClient *http.Client
}

// Embedder embeds text through the Gemini embedding API: embedContent
// for queries, batchEmbedContents for document batches. It implements
// embedding.Embedder and is safe for concurrent use.
type Embedder struct {
	cfg  EmbedderConfig
	http *http.Client
}

// NewEmbedder validates cfg and returns an Embedder ready for use.
func NewEmbedder(cfg EmbedderConfig) (*Embedder, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("gemini: API key is required")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("gemini: model is required")
	}
	if cfg.Dimensions < 0 {
		return nil, fmt.Errorf("gemini: dimensions must not be negative, got %d", cfg.Dimensions)
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Minute}
	}
	return &Embedder{cfg: cfg, http: httpClient}, nil
}

// EmbedQuery embeds one search query.
func (e *Embedder) EmbedQuery(ctx context.Context, text string) (embedding.Result, error) {
	if err := validateEmbedTexts([]string{text}); err != nil {
		return embedding.Result{}, err
	}
	body, err := json.Marshal(e.embedRequest(text, embedTaskQuery))
	if err != nil {
		return embedding.Result{}, &DecodeError{Stage: "encode request", Err: err}
	}
	payload, err := e.post(ctx, ":embedContent", body)
	if err != nil {
		return embedding.Result{}, err
	}
	var wire embedContentResponse
	if err := json.Unmarshal(payload, &wire); err != nil {
		return embedding.Result{}, &DecodeError{Stage: "decode response", Err: err}
	}
	return newEmbedResult([][]float32{wire.Embedding.Values}, wire.UsageMetadata, 1)
}

// EmbedDocuments embeds the batch in one batchEmbedContents call; the
// API returns the vectors in input order. Very large batches should be
// chunked by the application. Provider failures return *APIError,
// network-level failures return *TransportError, and unexpected
// response shapes return *DecodeError.
func (e *Embedder) EmbedDocuments(ctx context.Context, texts []string) (embedding.Result, error) {
	if err := validateEmbedTexts(texts); err != nil {
		return embedding.Result{}, err
	}
	requests := make([]embedContentRequest, 0, len(texts))
	for _, text := range texts {
		requests = append(requests, e.embedRequest(text, embedTaskDocument))
	}
	body, err := json.Marshal(batchEmbedRequest{Requests: requests})
	if err != nil {
		return embedding.Result{}, &DecodeError{Stage: "encode request", Err: err}
	}
	payload, err := e.post(ctx, ":batchEmbedContents", body)
	if err != nil {
		return embedding.Result{}, err
	}
	var wire batchEmbedResponse
	if err := json.Unmarshal(payload, &wire); err != nil {
		return embedding.Result{}, &DecodeError{Stage: "decode response", Err: err}
	}
	vectors := make([][]float32, 0, len(wire.Embeddings))
	for _, item := range wire.Embeddings {
		vectors = append(vectors, item.Values)
	}
	return newEmbedResult(vectors, wire.UsageMetadata, len(texts))
}

// embedRequest builds one text's request. The model rides both the URL
// and the body, per the wire contract, and the task type is always
// sent: the query/documents split is the adapter's task-type encoding.
func (e *Embedder) embedRequest(text, taskType string) embedContentRequest {
	return embedContentRequest{
		Model:                "models/" + e.cfg.Model,
		Content:              embedContent{Parts: []embedPart{{Text: text}}},
		TaskType:             taskType,
		OutputDimensionality: e.cfg.Dimensions,
	}
}

// post performs the call against the model's embed endpoint and
// returns the response payload for the caller's endpoint-specific
// decoder.
func (e *Embedder) post(ctx context.Context, method string, body []byte) ([]byte, error) {
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(e.cfg.BaseURL, "/")+"/v1beta/models/"+e.cfg.Model+method,
		bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("gemini: build request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("x-goog-api-key", e.cfg.APIKey)

	httpResponse, err := e.http.Do(httpRequest)
	if err != nil {
		return nil, &TransportError{Err: err}
	}
	defer httpResponse.Body.Close()

	payload, err := io.ReadAll(httpResponse.Body)
	if err != nil {
		return nil, &TransportError{Err: err}
	}
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		return nil, newAPIError(httpResponse.StatusCode, payload)
	}
	return payload, nil
}

// newEmbedResult verifies the batch arrived complete — one non-empty
// vector per input, in input order — and pairs it with the usage.
func newEmbedResult(vectors [][]float32, usage embeddingUsage, inputCount int) (embedding.Result, error) {
	if len(vectors) != inputCount {
		return embedding.Result{}, &DecodeError{Stage: "decode response", Err: fmt.Errorf(
			"embedding response carries %d vectors for %d inputs", len(vectors), inputCount)}
	}
	for i, vector := range vectors {
		if len(vector) == 0 {
			return embedding.Result{}, &DecodeError{Stage: "decode response", Err: fmt.Errorf(
				"embedding response %d carries an empty vector", i)}
		}
	}
	return embedding.Result{
		Vectors: vectors,
		Usage:   model.Usage{InputTokens: usage.PromptTokenCount},
	}, nil
}

// validateEmbedTexts rejects empty batches and empty strings before any
// network call: the provider rejects both with an unhelpful 400.
func validateEmbedTexts(texts []string) error {
	if len(texts) == 0 {
		return &DecodeError{Stage: "validate input", Err: fmt.Errorf("embedding input carries no texts")}
	}
	for i, text := range texts {
		if text == "" {
			return &DecodeError{Stage: "validate input", Err: fmt.Errorf("embedding input %d is empty", i)}
		}
	}
	return nil
}

// embedPart is one text part of the content to embed; only parts.text
// is counted by the API.
type embedPart struct {
	Text string `json:"text"`
}

// embedContent wraps the parts; the embeddings API takes text only.
type embedContent struct {
	Parts []embedPart `json:"parts"`
}

// embedContentRequest is one text's embed request.
type embedContentRequest struct {
	Model                string       `json:"model"`
	Content              embedContent `json:"content"`
	TaskType             string       `json:"taskType"`
	OutputDimensionality int          `json:"outputDimensionality,omitempty"`
}

// batchEmbedRequest embeds a list of texts in one call.
type batchEmbedRequest struct {
	Requests []embedContentRequest `json:"requests"`
}

// contentEmbedding is one output vector.
type contentEmbedding struct {
	Values []float32 `json:"values"`
}

// embeddingUsage is the embeddings usage vocabulary.
type embeddingUsage struct {
	PromptTokenCount int `json:"promptTokenCount"`
}

// embedContentResponse is the single-text embedContent response.
type embedContentResponse struct {
	Embedding     contentEmbedding `json:"embedding"`
	UsageMetadata embeddingUsage   `json:"usageMetadata"`
}

// batchEmbedResponse is the batchEmbedContents response; embeddings is
// ordered to match the request list.
type batchEmbedResponse struct {
	Embeddings    []contentEmbedding `json:"embeddings"`
	UsageMetadata embeddingUsage     `json:"usageMetadata"`
}
