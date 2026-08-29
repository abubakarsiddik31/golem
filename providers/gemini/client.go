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
	// Thinking requests reasoning; nil leaves the provider default. See
	// ThinkingConfig for the fields.
	Thinking *ThinkingConfig
	// HTTPClient performs requests; defaults to a client with a 5-minute
	// timeout. Callers wanting different timeout behavior supply their
	// own; cancellation always flows through ctx.
	HTTPClient *http.Client
}

// ThinkingConfig requests reasoning from Gemini models. IncludeThoughts
// returns thought summaries as parts; Budget bounds the tokens spent
// thinking (Gemini 2.5 models) and Level selects a qualitative effort
// ("LOW", "MEDIUM", "HIGH"; Gemini 3 models). Budget and Level are
// mutually exclusive; New rejects a config that sets both, a negative
// budget, or none of them.
type ThinkingConfig struct {
	// IncludeThoughts returns thought summaries alongside the answer.
	IncludeThoughts bool
	// Budget bounds the thinking token count; it must be positive when
	// set.
	Budget int
	// Level selects the thinking level instead of a budget.
	Level string
}

// validate enforces the field rules on a ThinkingConfig.
func (t *ThinkingConfig) validate() error {
	switch {
	case t.Budget < 0:
		return fmt.Errorf("gemini: thinking budget must not be negative, got %d", t.Budget)
	case t.Budget > 0 && t.Level != "":
		return fmt.Errorf("gemini: thinking config must set Budget or Level, not both")
	case t.Budget == 0 && t.Level == "" && !t.IncludeThoughts:
		return fmt.Errorf("gemini: thinking config must set at least one of IncludeThoughts, Budget, or Level")
	}
	return nil
}

// wire converts the validated config to its request shape.
func (t *ThinkingConfig) wire() wireThinkingConfig {
	return wireThinkingConfig{
		IncludeThoughts: t.IncludeThoughts,
		ThinkingBudget:  t.Budget,
		ThinkingLevel:   t.Level,
	}
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
	if err := validateSampling(cfg.Temperature, cfg.TopP); err != nil {
		return nil, err
	}
	if cfg.MaxTokens < 0 {
		return nil, fmt.Errorf("gemini: max tokens must not be negative, got %d", cfg.MaxTokens)
	}
	if cfg.Thinking != nil {
		if err := cfg.Thinking.validate(); err != nil {
			return nil, err
		}
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
	// The GenerateContent API rejects JSON response mode combined with
	// function calling; reject the combination before any network call
	// instead of letting the provider answer a cryptic 400 mid-run.
	if len(request.OutputSchema) > 0 && len(request.ToolSpecs) > 0 {
		return nil, &DecodeError{Stage: "encode request", Err: fmt.Errorf(
			"output schema cannot be combined with tool declarations: JSON response mode is unsupported with function calling; configure tool-mode output (golem.WithOutputTool) instead")}
	}
	system, contents := toWireContents(request.Messages)
	var generationConfig *wireGenConfig
	if len(request.OutputSchema) > 0 || c.cfg.Temperature != nil || c.cfg.TopP != nil || c.cfg.MaxTokens > 0 || c.cfg.Thinking != nil {
		generationConfig = &wireGenConfig{
			Temperature:     c.cfg.Temperature,
			TopP:            c.cfg.TopP,
			MaxOutputTokens: c.cfg.MaxTokens,
		}
		if c.cfg.Thinking != nil {
			thinking := c.cfg.Thinking.wire()
			generationConfig.ThinkingConfig = &thinking
		}
		if len(request.OutputSchema) > 0 {
			generationConfig.ResponseMimeType = "application/json"
			generationConfig.ResponseSchema = request.OutputSchema
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

// validateSampling checks the optional sampling controls against the
// provider's documented ranges.
func validateSampling(temperature, topP *float64) error {
	if temperature != nil && (*temperature < 0 || *temperature > 2) {
		return fmt.Errorf("gemini: temperature must be in [0, 2], got %v", *temperature)
	}
	if topP != nil && (*topP < 0 || *topP > 1) {
		return fmt.Errorf("gemini: top P must be in [0, 1], got %v", *topP)
	}
	return nil
}
