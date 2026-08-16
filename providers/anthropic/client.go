// Package anthropic adapts the Anthropic Messages API to Golem's
// provider-neutral model contract.
//
// The package is stdlib-only transport, explicit configuration, typed
// error classification, and normalization of provider encodings into the
// model contract. Request.OutputSchema is currently ignored: the Messages
// API has no response-format field, so structured output stays
// decoder-validated with correction rounds.
package anthropic

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

const (
	// DefaultBaseURL is the Anthropic Messages API host.
	DefaultBaseURL = "https://api.anthropic.com"
	// DefaultMaxTokens bounds generation when Config.MaxTokens is unset.
	// The Messages API requires a positive value.
	DefaultMaxTokens = 1024
	// apiVersion pins the Messages API wire version.
	apiVersion = "2023-06-01"
)

// Config configures a Client. APIKey and Model are required; the zero
// values of the remaining fields select documented defaults. There are no
// implicit environment reads: callers wire os.Getenv themselves.
type Config struct {
	// APIKey authenticates requests via the x-api-key header.
	APIKey string
	// BaseURL prefixes the messages path; defaults to DefaultBaseURL.
	// Point it at an API-compatible proxy when needed.
	BaseURL string
	// Model names the model to generate with, e.g. "claude-sonnet-4-5".
	Model string
	// MaxTokens bounds the tokens the model may generate per response.
	// The Messages API requires a positive value; zero selects
	// DefaultMaxTokens and negative values fail New.
	MaxTokens int
	// HTTPClient performs requests; defaults to a client with a 5-minute
	// timeout. Callers wanting different timeout behavior supply their
	// own; cancellation always flows through ctx.
	HTTPClient *http.Client
}

// Client generates responses through the Anthropic Messages API. It
// implements model.Model and is safe for concurrent use.
type Client struct {
	cfg  Config
	http *http.Client
}

// New validates cfg and returns a Client ready for use with an agent.
func New(cfg Config) (*Client, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("anthropic: API key is required")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("anthropic: model is required")
	}
	if cfg.MaxTokens < 0 {
		return nil, fmt.Errorf("anthropic: max tokens must not be negative, got %d", cfg.MaxTokens)
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = DefaultMaxTokens
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Minute}
	}
	return &Client{cfg: cfg, http: httpClient}, nil
}

// Generate translates request to the Messages API wire format, calls the
// API, and normalizes the response. Provider failures return *APIError,
// network-level failures return *TransportError, and unexpected response
// shapes return *DecodeError.
func (c *Client) Generate(ctx context.Context, request model.Request) (model.Response, error) {
	httpRequest, err := c.newMessagesHTTPRequest(ctx, request, false)
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

	return fromWireResponse(payload)
}

// newMessagesHTTPRequest builds the POST request for the messages
// endpoint. stream selects streaming mode.
func (c *Client) newMessagesHTTPRequest(ctx context.Context, request model.Request, stream bool) (*http.Request, error) {
	system, turns := toWireTurns(request.Messages)
	body, err := json.Marshal(messagesRequest{
		Model:     c.cfg.Model,
		MaxTokens: c.cfg.MaxTokens,
		System:    system,
		Messages:  turns,
		Tools:     toWireTools(request.ToolSpecs),
		Stream:    stream,
	})
	if err != nil {
		return nil, &DecodeError{Stage: "encode request", Err: err}
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(c.cfg.BaseURL, "/")+"/v1/messages",
		bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("anthropic: build request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("x-api-key", c.cfg.APIKey)
	httpRequest.Header.Set("anthropic-version", apiVersion)
	return httpRequest, nil
}
