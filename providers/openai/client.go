// Package openai adapts OpenAI-compatible chat-completions APIs to
// Golem's provider-neutral model contract. A configurable BaseURL makes
// the same adapter serve Groq, OpenRouter, DeepSeek, Together, Ollama,
// and vLLM.
//
// The package is stdlib-only transport, explicit
// configuration, typed error classification, and normalization of provider
// encodings into the model contract.
package openai

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

// DefaultBaseURL is the OpenAI chat-completions endpoint prefix.
const DefaultBaseURL = "https://api.openai.com/v1"

// Config configures a Client. APIKey and Model are required; the zero
// values of the remaining fields select documented defaults. There are no
// implicit environment reads: callers wire os.Getenv themselves.
type Config struct {
	// APIKey authenticates requests via the Authorization header.
	APIKey string
	// BaseURL prefixes the chat-completions path; defaults to
	// DefaultBaseURL.
	BaseURL string
	// Model names the model to generate with, e.g. "gpt-4o".
	Model string
	// HTTPClient performs requests; defaults to a client with a 5-minute
	// timeout. Callers wanting different timeout behavior supply their
	// own; cancellation always flows through ctx.
	HTTPClient *http.Client
}

// Client generates responses through an OpenAI-compatible
// chat-completions API. It implements model.Model and is safe for
// concurrent use.
type Client struct {
	cfg  Config
	http *http.Client
}

// New validates cfg and returns a Client ready for use with an agent.
func New(cfg Config) (*Client, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("openai: API key is required")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("openai: model is required")
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

// Generate translates request to the chat-completions wire format, calls
// the API, and normalizes the response. Provider failures return *APIError,
// network-level failures return *TransportError, and unexpected response
// shapes return *DecodeError.
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

// newChatHTTPRequest builds the POST request for the chat-completions
// endpoint. stream selects streaming mode, which also requests usage in
// the final chunk.
func (c *Client) newChatHTTPRequest(ctx context.Context, request model.Request, stream bool) (*http.Request, error) {
	var streamOptions *chatStreamOptions
	if stream {
		streamOptions = &chatStreamOptions{IncludeUsage: true}
	}
	body, err := json.Marshal(chatRequest{
		Model:         c.cfg.Model,
		Messages:      toWireMessages(request.Messages),
		Tools:         toWireTools(request.ToolSpecs),
		Stream:        stream,
		StreamOptions: streamOptions,
	})
	if err != nil {
		return nil, &DecodeError{Stage: "encode request", Err: err}
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(c.cfg.BaseURL, "/")+"/chat/completions",
		bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai: build request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	return httpRequest, nil
}
