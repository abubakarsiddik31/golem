// Package azure adapts Azure OpenAI chat completions to Golem's
// provider-neutral model contract.
//
// The wire format matches OpenAI chat completions; what differs is the
// addressing — per-resource endpoints, named deployments, and an explicit
// api-version query parameter — and the api-key authentication header.
//
// The package is stdlib-only transport, explicit configuration, typed
// error classification, and normalization of provider encodings into the
// model contract.
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

	"github.com/abubakarsiddik31/golem/model"
)

// Config configures a Client. APIKey, Endpoint, Deployment, and
// APIVersion are all required; there is no implicit default API version,
// because versions gate feature support. There are no implicit
// environment reads: callers wire os.Getenv themselves.
type Config struct {
	// APIKey authenticates requests via the api-key header.
	APIKey string
	// Endpoint is the resource endpoint, e.g.
	// "https://my-resource.openai.azure.com". Sovereign clouds use their
	// own hosts.
	Endpoint string
	// Deployment names the model deployment to generate with.
	Deployment string
	// APIVersion selects the data-plane API version, e.g. "2024-10-21".
	// Structured output (response_format json_schema) requires a version
	// that supports it.
	APIVersion string
	// Temperature controls sampling randomness in [0, 2]; nil leaves the
	// provider default. Set it with providers.Ptr, including to 0.
	Temperature *float64
	// TopP restricts sampling to the nucleus probability mass in [0, 1];
	// nil leaves the provider default.
	TopP *float64
	// MaxTokens bounds the tokens the model may generate per response.
	// Zero omits the bound and lets the provider default apply; negative
	// values fail New.
	MaxTokens int
	// HTTPClient performs requests; defaults to a client with a 5-minute
	// timeout. Callers wanting different timeout behavior supply their
	// own; cancellation always flows through ctx.
	HTTPClient *http.Client
}

// Client generates responses through Azure OpenAI chat completions. It
// implements model.Model and is safe for concurrent use.
type Client struct {
	cfg  Config
	http *http.Client
}

// New validates cfg and returns a Client ready for use with an agent.
func New(cfg Config) (*Client, error) {
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
	if err := validateSampling(cfg.Temperature, cfg.TopP); err != nil {
		return nil, err
	}
	if cfg.MaxTokens < 0 {
		return nil, fmt.Errorf("azure: max tokens must not be negative, got %d", cfg.MaxTokens)
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Minute}
	}
	return &Client{cfg: cfg, http: httpClient}, nil
}

// Generate translates request to the chat-completions wire format, calls
// the deployment, and normalizes the response. Provider failures return
// *APIError, network-level failures return *TransportError, and
// unexpected response shapes return *DecodeError.
func (c *Client) Generate(ctx context.Context, request model.Request) (model.Response, error) {
	httpRequest, err := c.newChatHTTPRequest(ctx, request, false)
	if err != nil {
		return model.Response{}, err
	}

	httpResponse, err := c.http.Do(httpRequest)
	if err != nil {
		return model.Response{}, &TransportError{Err: err}
	}
	defer httpResponse.Body.Close()

	payload, err := io.ReadAll(httpResponse.Body)
	if err != nil {
		return model.Response{}, &TransportError{Err: err}
	}
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		return model.Response{}, newAPIError(httpResponse.StatusCode, payload)
	}

	response, err := fromWireResponse(payload)
	if err != nil {
		return model.Response{}, err
	}
	return response, nil
}

// newChatHTTPRequest builds the POST request for the deployment's chat
// endpoint. stream selects streaming mode, which also requests usage in
// the final chunk. A request carrying an output schema maps to a strict
// json_schema response format.
func (c *Client) newChatHTTPRequest(ctx context.Context, request model.Request, stream bool) (*http.Request, error) {
	var streamOptions *chatStreamOptions
	if stream {
		streamOptions = &chatStreamOptions{IncludeUsage: true}
	}
	var responseFormat *chatResponseFormat
	if len(request.OutputSchema) > 0 {
		responseFormat = &chatResponseFormat{
			Type:       "json_schema",
			JSONSchema: &chatJSONSchema{Name: "output", Strict: true, Schema: request.OutputSchema},
		}
	}
	body, err := json.Marshal(chatRequest{
		Messages:       toWireMessages(request.Messages),
		Tools:          toWireTools(request.ToolSpecs),
		Temperature:    c.cfg.Temperature,
		TopP:           c.cfg.TopP,
		MaxTokens:      c.cfg.MaxTokens,
		Stream:         stream,
		StreamOptions:  streamOptions,
		ResponseFormat: responseFormat,
	})
	if err != nil {
		return nil, &DecodeError{Stage: "encode request", Err: err}
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(c.cfg.Endpoint, "/")+
			"/openai/deployments/"+c.cfg.Deployment+"/chat/completions?api-version="+c.cfg.APIVersion,
		bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("azure: build request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("api-key", c.cfg.APIKey)
	return httpRequest, nil
}

// validateSampling checks the optional sampling controls against the
// provider's documented ranges.
func validateSampling(temperature, topP *float64) error {
	if temperature != nil && (*temperature < 0 || *temperature > 2) {
		return fmt.Errorf("azure: temperature must be in [0, 2], got %v", *temperature)
	}
	if topP != nil && (*topP < 0 || *topP > 1) {
		return fmt.Errorf("azure: top P must be in [0, 1], got %v", *topP)
	}
	return nil
}
