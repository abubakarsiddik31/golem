// Package anthropic adapts the Anthropic Messages API to Golem's
// provider-neutral model contract.
//
// The package is stdlib-only transport, explicit configuration, typed
// error classification, and normalization of provider encodings into the
// model contract.
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
	// Temperature controls sampling randomness in [0, 1]; nil leaves the
	// provider default. Set it with providers.Ptr, including to 0.
	Temperature *float64
	// TopP restricts sampling to the nucleus probability mass in [0, 1];
	// nil leaves the provider default.
	TopP *float64
	// Thinking requests reasoning; nil leaves the provider default, which
	// on Claude Sonnet 5 and Opus 5 is adaptive thinking.
	Thinking *ThinkingConfig
	// Effort scales how much the model reasons and works, e.g. "low",
	// "medium", "high", or "xhigh"; empty leaves the provider default. It
	// applies with adaptive thinking and on its own.
	Effort string
	// HTTPClient performs requests; defaults to a client with a 5-minute
	// timeout. Callers wanting different timeout behavior supply their
	// own; cancellation always flows through ctx.
	HTTPClient *http.Client
}

// ThinkingConfig requests reasoning from Anthropic models. Exactly one
// field is set; the zero config is invalid and New rejects it. Adaptive
// thinking (Claude Opus 4.6 and later) lets the model decide when and how
// much to think; older models require a BudgetTokens budget instead.
// Thinking-by-default models need Disabled to turn thinking off, because
// omitting the thinking field selects adaptive thinking on them.
type ThinkingConfig struct {
	// Adaptive enables adaptive thinking.
	Adaptive bool
	// BudgetTokens bounds extended thinking on models that take a fixed
	// budget; it must be positive when set. Deprecated by the provider on
	// Claude Opus 4.6 and later in favor of Adaptive.
	BudgetTokens int
	// Disabled turns thinking off explicitly.
	Disabled bool
}

// wire converts the validated config to its request shape, choosing the
// enabled form when a budget is set.
func (t *ThinkingConfig) wire() wireThinking {
	if t.BudgetTokens > 0 {
		return wireThinking{Type: "enabled", BudgetTokens: t.BudgetTokens}
	}
	if t.Disabled {
		return wireThinking{Type: "disabled"}
	}
	return wireThinking{Type: "adaptive"}
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
	if err := validateSampling(cfg.Temperature, cfg.TopP); err != nil {
		return nil, err
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
// endpoint. stream selects streaming mode. A request carrying an output
// schema maps to the json_schema output format.
func (c *Client) newMessagesHTTPRequest(ctx context.Context, request model.Request, stream bool) (*http.Request, error) {
	system, turns := toWireTurns(request.Messages)
	var outputConfig *wireOutputConfig
	if len(request.OutputSchema) > 0 {
		outputConfig = &wireOutputConfig{Format: &wireOutputFormat{
			Type:   "json_schema",
			Schema: request.OutputSchema,
		}}
	}
	body, err := json.Marshal(messagesRequest{
		Model:        c.cfg.Model,
		MaxTokens:    c.cfg.MaxTokens,
		System:       system,
		Messages:     turns,
		Tools:        toWireTools(request.ToolSpecs),
		Temperature:  c.cfg.Temperature,
		TopP:         c.cfg.TopP,
		Thinking:     thinkingOnWire(c.cfg.Thinking),
		Effort:       c.cfg.Effort,
		OutputConfig: outputConfig,
		Stream:       stream,
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

// validateSampling checks the optional sampling controls against the
// provider's documented ranges.
func validateSampling(temperature, topP *float64) error {
	if temperature != nil && (*temperature < 0 || *temperature > 1) {
		return fmt.Errorf("anthropic: temperature must be in [0, 1], got %v", *temperature)
	}
	if topP != nil && (*topP < 0 || *topP > 1) {
		return fmt.Errorf("anthropic: top P must be in [0, 1], got %v", *topP)
	}
	return nil
}

// validate enforces the exactly-one-field rule on a ThinkingConfig.
func (t *ThinkingConfig) validate() error {
	set := 0
	for _, chosen := range []bool{t.Adaptive, t.BudgetTokens > 0, t.Disabled} {
		if chosen {
			set++
		}
	}
	switch {
	case set == 0:
		return fmt.Errorf("anthropic: thinking config must set one of Adaptive, BudgetTokens, or Disabled")
	case set > 1:
		return fmt.Errorf("anthropic: thinking config must set exactly one of Adaptive, BudgetTokens, or Disabled")
	case t.BudgetTokens < 0:
		return fmt.Errorf("anthropic: thinking budget must not be negative, got %d", t.BudgetTokens)
	}
	return nil
}

// thinkingOnWire maps a validated config to its request shape; nil keeps
// thinking off the wire entirely.
func thinkingOnWire(config *ThinkingConfig) *wireThinking {
	if config == nil {
		return nil
	}
	wire := config.wire()
	return &wire
}
