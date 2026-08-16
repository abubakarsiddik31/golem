// Package gemini adapts the Google Gemini GenerateContent API to Golem's
// provider-neutral model contract.
//
// The package is stdlib-only transport, explicit configuration, typed
// error classification, and normalization of provider encodings into the
// model contract. Function calls on the wire carry no ID; the adapter
// generates stable ones and correlates function responses by tool name.
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

	"github.com/abubakarsiddik31/golem/model"
)

// DefaultBaseURL is the Generative Language API host.
const DefaultBaseURL = "https://generativelanguage.googleapis.com"

// Config configures a Client. APIKey and Model are required; the zero
// values of the remaining fields select documented defaults. There are no
// implicit environment reads: callers wire os.Getenv themselves.
type Config struct {
	// APIKey authenticates requests via the x-goog-api-key header.
	APIKey string
	// BaseURL prefixes the model path; defaults to DefaultBaseURL. Point
	// it at an API-compatible proxy when needed.
	BaseURL string
	// Model names the model to generate with, e.g.
	// "gemini-2.5-flash".
	Model string
	// HTTPClient performs requests; defaults to a client with a 5-minute
	// timeout. Callers wanting different timeout behavior supply their
	// own; cancellation always flows through ctx.
	HTTPClient *http.Client
}

// Client generates responses through the Gemini GenerateContent API. It
// implements model.Model and is safe for concurrent use.
type Client struct {
	cfg  Config
	http *http.Client
}

// New validates cfg and returns a Client ready for use with an agent.
func New(cfg Config) (*Client, error) {
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
	return &Client{cfg: cfg, http: httpClient}, nil
}

// Generate translates request to the GenerateContent wire format, calls
// the API, and normalizes the response. Provider failures return
// *APIError, network-level failures return *TransportError, and
// unexpected response shapes return *DecodeError.
func (c *Client) Generate(ctx context.Context, request model.Request) (model.Response, error) {
	httpRequest, err := c.newGenerateContentHTTPRequest(ctx, request, false)
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

// newGenerateContentHTTPRequest builds the POST request for the model's
// generateContent or streamGenerateContent endpoint. A request carrying
// an output schema selects JSON responses shaped by that schema.
func (c *Client) newGenerateContentHTTPRequest(ctx context.Context, request model.Request, stream bool) (*http.Request, error) {
	system, contents := toWireContents(request.Messages)
	var generationConfig *wireGenConfig
	if len(request.OutputSchema) > 0 {
		generationConfig = &wireGenConfig{
			ResponseMimeType: "application/json",
			ResponseSchema:   request.OutputSchema,
		}
	}
	body, err := json.Marshal(generateContentRequest{
		Contents:          contents,
		SystemInstruction: system,
		Tools:             toWireTools(request.ToolSpecs),
		GenerationConfig:  generationConfig,
	})
	if err != nil {
		return nil, &DecodeError{Stage: "encode request", Err: err}
	}

	method := "generateContent"
	if stream {
		method = "streamGenerateContent?alt=sse"
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(c.cfg.BaseURL, "/")+"/v1beta/models/"+c.cfg.Model+":"+method,
		bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("gemini: build request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("x-goog-api-key", c.cfg.APIKey)
	return httpRequest, nil
}
