package azure

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

// EmbedderConfig configures an Embedder. APIKey, Endpoint, Deployment,
// and APIVersion are all required; there is no implicit default API
// version, because versions gate feature support. There are no implicit
// environment reads: callers wire os.Getenv themselves.
type EmbedderConfig struct {
	// APIKey authenticates requests via the api-key header.
	APIKey string
	// Endpoint is the resource endpoint, e.g.
	// "https://my-resource.openai.azure.com". Sovereign clouds use their
	// own hosts.
	Endpoint string
	// Deployment names the embedding-model deployment to embed with.
	Deployment string
	// APIVersion selects the data-plane API version, e.g. "2024-10-21".
	APIVersion string
	// Dimensions truncates the output vectors to the given width.
	// Only text-embedding-3 and later models accept it; zero omits the
	// field and lets the provider default apply; negative values fail
	// NewEmbedder.
	Dimensions int
	// HTTPClient performs requests; defaults to a client with a 5-minute
	// timeout. Callers wanting different timeout behavior supply their
	// own; cancellation always flows through ctx.
	HTTPClient *http.Client
}

// Embedder embeds text through an Azure OpenAI deployment. It
// implements embedding.Embedder and is safe for concurrent use.
type Embedder struct {
	cfg  EmbedderConfig
	http *http.Client
}

// NewEmbedder validates cfg and returns an Embedder ready for use.
func NewEmbedder(cfg EmbedderConfig) (*Embedder, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("azure: API key is required")
	}
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("azure: endpoint is required")
	}
	if cfg.Deployment == "" {
		return nil, fmt.Errorf("azure: deployment is required")
	}
	if cfg.APIVersion == "" {
		return nil, fmt.Errorf("azure: API version is required")
	}
	if cfg.Dimensions < 0 {
		return nil, fmt.Errorf("azure: dimensions must not be negative, got %d", cfg.Dimensions)
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Minute}
	}
	return &Embedder{cfg: cfg, http: httpClient}, nil
}

// EmbedDocuments embeds the batch in one deployment call. Azure
// OpenAI's request caps match the OpenAI API it fronts; chunking past
// those limits stays with the application. Provider failures return
// *APIError, network-level failures return *TransportError, and
// unexpected response shapes return *DecodeError.
func (e *Embedder) EmbedDocuments(ctx context.Context, texts []string) (embedding.Result, error) {
	return e.embed(ctx, texts)
}

// EmbedQuery embeds one search query.
func (e *Embedder) EmbedQuery(ctx context.Context, text string) (embedding.Result, error) {
	return e.embed(ctx, []string{text})
}

// embed posts the deployment request; the query/documents split has no
// wire equivalent on the Azure OpenAI embeddings endpoint.
func (e *Embedder) embed(ctx context.Context, texts []string) (embedding.Result, error) {
	if err := validateEmbedTexts(texts); err != nil {
		return embedding.Result{}, err
	}
	body, err := json.Marshal(embedRequest{Input: texts, Dimensions: e.cfg.Dimensions})
	if err != nil {
		return embedding.Result{}, &DecodeError{Stage: "encode request", Err: err}
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(e.cfg.Endpoint, "/")+"/openai/deployments/"+e.cfg.Deployment+"/embeddings?api-version="+e.cfg.APIVersion,
		bytes.NewReader(body))
	if err != nil {
		return embedding.Result{}, fmt.Errorf("azure: build request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("api-key", e.cfg.APIKey)

	httpResponse, err := e.http.Do(httpRequest)
	if err != nil {
		return embedding.Result{}, &TransportError{Err: err}
	}
	defer httpResponse.Body.Close()

	payload, err := io.ReadAll(httpResponse.Body)
	if err != nil {
		return embedding.Result{}, &TransportError{Err: err}
	}
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		return embedding.Result{}, newAPIError(httpResponse.StatusCode, payload)
	}

	var wire embedResponse
	if err := json.Unmarshal(payload, &wire); err != nil {
		return embedding.Result{}, &DecodeError{Stage: "decode response", Err: err}
	}
	if len(wire.Data) != len(texts) {
		return embedding.Result{}, &DecodeError{Stage: "decode response", Err: fmt.Errorf(
			"embedding response carries %d vectors for %d inputs", len(wire.Data), len(texts))}
	}
	vectors := make([][]float32, 0, len(wire.Data))
	for i, item := range wire.Data {
		if len(item.Embedding) == 0 {
			return embedding.Result{}, &DecodeError{Stage: "decode response", Err: fmt.Errorf(
				"embedding response %d carries an empty vector", i)}
		}
		vectors = append(vectors, item.Embedding)
	}
	return embedding.Result{
		Vectors: vectors,
		Usage:   model.Usage{InputTokens: wire.Usage.PromptTokens},
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

// embedRequest is the embeddings wire request. The deployment names the
// model, so no model field rides the body; encoding_format stays unset:
// the float default is the only shape the adapter decodes.
type embedRequest struct {
	Input      []string `json:"input"`
	Dimensions int      `json:"dimensions,omitempty"`
}

// embedResponse is the embeddings wire response; data is ordered by
// index, matching the input order.
type embedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}
