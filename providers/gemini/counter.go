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

	"github.com/abubakarsiddik31/golem/tokens"
)

// Counter counts input tokens through the Gemini countTokens endpoint.
// It implements tokens.Counter and is safe for concurrent use.
type Counter struct {
	cfg  Config
	http *http.Client
}

// NewCounter validates cfg and returns a Counter. APIKey and Model are
// required; BaseURL and HTTPClient carry the same meaning as on Config
// for Client. The count prices what a GenerateContent request with the
// same model would consume — contents, system instruction, and tool
// declarations included; a would-be response is never included.
func NewCounter(cfg Config) (*Counter, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("gemini: API key is required")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("gemini: model is required")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Minute}
	}
	return &Counter{cfg: cfg, http: httpClient}, nil
}

// countRequest carries the fields the countTokens endpoint takes: the
// same contents shape the inference request uses, minus generation
// controls.
type countRequest struct {
	Contents          []wireContent  `json:"contents"`
	SystemInstruction *wireSystem    `json:"systemInstruction,omitempty"`
	Tools             []wireToolList `json:"tools,omitempty"`
}

// countResponse is the endpoint's reply.
type countResponse struct {
	TotalTokens int `json:"totalTokens"`
}

// CountTokens prices input as the provider reports it. Provider
// failures return *APIError, network-level failures return
// *TransportError, and unexpected response shapes return *DecodeError.
func (c *Counter) CountTokens(ctx context.Context, input tokens.CountInput) (int, error) {
	system, contents, err := toWireContents(input.Messages)
	if err != nil {
		return 0, err
	}
	body, err := json.Marshal(countRequest{
		Contents:          contents,
		SystemInstruction: system,
		Tools:             toWireTools(input.Tools),
	})
	if err != nil {
		return 0, &DecodeError{Stage: "encode request", Err: err}
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(c.cfg.BaseURL, "/")+"/v1beta/models/"+c.cfg.Model+":countTokens",
		bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("gemini: build request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("x-goog-api-key", c.cfg.APIKey)

	httpResponse, err := c.http.Do(httpRequest)
	if err != nil {
		return 0, &TransportError{Err: err}
	}
	defer httpResponse.Body.Close()

	payload, err := io.ReadAll(httpResponse.Body)
	if err != nil {
		return 0, &TransportError{Err: err}
	}
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		return 0, newAPIError(httpResponse.StatusCode, payload)
	}
	var decoded countResponse
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return 0, &DecodeError{Stage: "decode response", Err: err}
	}
	return decoded.TotalTokens, nil
}
