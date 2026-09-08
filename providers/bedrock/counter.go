package bedrock

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/abubakarsiddik31/golem/tokens"
)

// Counter counts input tokens through the Bedrock CountTokens API. It
// implements tokens.Counter and is safe for concurrent use.
type Counter struct {
	cfg  Config
	http *http.Client
}

// NewCounter validates cfg and returns a Counter. Credentials, Region,
// and Model are required; BaseURL and HTTPClient carry the same meaning
// as on Config for Client. The endpoint is free of charge and prices
// the messages and system guidance a Converse request with the same
// model would carry; tool definitions are not part of its request
// shape, so the count is a lower bound when the run advertises tools.
// AWS rejects counting for inference-profile model IDs (the "us." /
// "eu." / "global." prefixes) and for models without CountTokens
// support; use the base foundation-model ID.
func NewCounter(cfg Config) (*Counter, error) {
	if cfg.Credentials.AccessKeyID == "" {
		return nil, fmt.Errorf("bedrock: access key ID is required")
	}
	if cfg.Credentials.SecretAccessKey == "" {
		return nil, fmt.Errorf("bedrock: secret access key is required")
	}
	if cfg.Region == "" {
		return nil, fmt.Errorf("bedrock: region is required")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("bedrock: model is required")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com", cfg.Region)
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Minute}
	}
	return &Counter{cfg: cfg, http: httpClient}, nil
}

// countRequest wraps the Converse-shaped input the CountTokens API
// takes.
type countRequest struct {
	Input countInput `json:"input"`
}

type countInput struct {
	Converse converseInput `json:"converse"`
}

type converseInput struct {
	Messages []wireMessage `json:"messages"`
	System   []wireSystem  `json:"system,omitempty"`
}

// countResponse is the API's reply.
type countResponse struct {
	InputTokens int `json:"inputTokens"`
}

// CountTokens prices input as the provider reports it. Provider
// failures return *APIError, network-level failures return
// *TransportError, and unexpected response shapes return *DecodeError.
func (c *Counter) CountTokens(ctx context.Context, input tokens.CountInput) (int, error) {
	system, turns, err := toWireMessages(input.Messages)
	if err != nil {
		return 0, err
	}
	body, err := json.Marshal(countRequest{
		Input: countInput{Converse: converseInput{Messages: turns, System: system}},
	})
	if err != nil {
		return 0, &DecodeError{Stage: "encode request", Err: err}
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.cfg.BaseURL+"/model/"+c.cfg.Model+"/count-tokens",
		bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("bedrock: build request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	signV4(httpRequest, body, c.cfg.Credentials, c.cfg.Region, serviceName, time.Now())

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
		return 0, newAPIError(httpResponse.StatusCode, payload, httpResponse.Header.Get("x-amzn-errortype"))
	}
	var decoded countResponse
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return 0, &DecodeError{Stage: "decode response", Err: err}
	}
	return decoded.InputTokens, nil
}
