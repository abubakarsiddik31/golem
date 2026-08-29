// Package bedrock adapts the AWS Bedrock Runtime Converse API to Golem's
// provider-neutral model contract.
//
// The package is stdlib-only transport, including AWS Signature Version 4
// request signing, explicit configuration, typed error classification,
// and normalization of provider encodings into the model contract.
// GenerateStream speaks ConverseStream, whose responses are AWS binary
// event-stream framed rather than SSE.
//
// Credentials are wired in explicitly — the adapter never reads the AWS
// environment or credential chain. Request.OutputSchema maps to the
// Converse outputConfig json_schema format.
package bedrock

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/abubakarsiddik31/golem/model"
)

// serviceName identifies the signing target.
const serviceName = "bedrock"

// Config configures a Client. Credentials, Region, and Model are
// required; the zero values of the remaining fields select documented
// defaults.
type Config struct {
	// Credentials authenticate requests via AWS Signature Version 4.
	Credentials Credentials
	// Region routes requests to a regional endpoint, e.g. "us-east-1".
	Region string
	// Model names the model, inference profile, or ARN to generate with,
	// e.g. "anthropic.claude-sonnet-4-5-20250929-v1:0" or
	// "us.anthropic.claude-sonnet-4-5-20250929-v1:0".
	Model string
	// MaxTokens bounds the tokens the model may generate per response.
	// Zero omits the bound and lets the provider default apply; negative
	// values fail New.
	MaxTokens int
	// Temperature controls sampling randomness in [0, 1]; nil leaves the
	// provider default. Set it with providers.Ptr, including to 0.
	Temperature *float64
	// TopP restricts sampling to the nucleus probability mass in [0, 1];
	// nil leaves the provider default.
	TopP *float64
	// Thinking requests reasoning from Claude models via
	// additionalModelRequestFields; nil leaves the provider default. See
	// ThinkingConfig for the model-family constraints.
	Thinking *ThinkingConfig
	// Effort scales how much the model reasons and works, e.g. "low",
	// "medium", "high", or "xhigh"; empty leaves the provider default. It
	// rides outputConfig.effort and pairs with adaptive thinking.
	Effort string
	// BaseURL overrides the regional endpoint prefix, which defaults to
	// https://bedrock-runtime.{Region}.amazonaws.com. Point it at a
	// proxy or LocalStack-style emulator when needed.
	BaseURL string
	// HTTPClient performs requests; defaults to a client with a 5-minute
	// timeout. Callers wanting different timeout behavior supply their
	// own; cancellation always flows through ctx.
	HTTPClient *http.Client
}

// Client generates responses through the Bedrock Runtime Converse API. It
// implements model.Model and is safe for concurrent use.
type Client struct {
	cfg  Config
	http *http.Client
}

// New validates cfg and returns a Client ready for use with an agent.
func New(cfg Config) (*Client, error) {
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
	if cfg.MaxTokens < 0 {
		return nil, fmt.Errorf("bedrock: max tokens must not be negative, got %d", cfg.MaxTokens)
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
		cfg.BaseURL = fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com", cfg.Region)
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Minute}
	}
	return &Client{cfg: cfg, http: httpClient}, nil
}

// Generate translates request to the Converse wire format, signs and
// calls the API, and normalizes the response. Provider failures return
// *APIError, network-level failures return *TransportError, and
// unexpected response shapes return *DecodeError.
func (c *Client) Generate(ctx context.Context, request model.Request) (model.Response, error) {
	httpRequest, err := c.newConverseHTTPRequest(ctx, request, "/converse")
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
		return model.Response{}, newAPIError(httpResponse.StatusCode, payload, httpResponse.Header.Get("x-amzn-errortype"))
	}

	return fromWireResponse(payload)
}

// newConverseHTTPRequest builds the signed POST request for the model's
// converse endpoint — path selects the synchronous or streaming
// variant. A request carrying an output schema maps to the json_schema
// output format.
func (c *Client) newConverseHTTPRequest(ctx context.Context, request model.Request, path string) (*http.Request, error) {
	system, turns, err := toWireMessages(request.Messages)
	if err != nil {
		return nil, err
	}
	var inferenceConfig *wireInference
	if c.cfg.MaxTokens > 0 || c.cfg.Temperature != nil || c.cfg.TopP != nil {
		inferenceConfig = &wireInference{
			MaxTokens:   c.cfg.MaxTokens,
			Temperature: c.cfg.Temperature,
			TopP:        c.cfg.TopP,
		}
	}
	var outputConfig *wireOutputConfig
	if len(request.OutputSchema) > 0 {
		outputConfig = &wireOutputConfig{TextFormat: &wireTextFormat{
			Type: "json_schema",
			Structure: &wireFormatStructure{JSONSchema: &wireJSONSchemaDef{
				Name:   "output",
				Schema: string(request.OutputSchema),
			}},
		}}
	}
	if c.cfg.Effort != "" {
		if outputConfig == nil {
			outputConfig = &wireOutputConfig{}
		}
		outputConfig.Effort = c.cfg.Effort
	}
	body, err := json.Marshal(converseRequest{
		Messages:                     turns,
		System:                       system,
		ToolConfig:                   toWireToolConfig(request.ToolSpecs),
		InferenceConfig:              inferenceConfig,
		OutputConfig:                 outputConfig,
		AdditionalModelRequestFields: c.cfg.Thinking.wireFields(),
	})
	if err != nil {
		return nil, &DecodeError{Stage: "encode request", Err: err}
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.cfg.BaseURL+"/model/"+c.cfg.Model+path,
		bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("bedrock: build request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	signV4(httpRequest, body, c.cfg.Credentials, c.cfg.Region, serviceName, time.Now())
	return httpRequest, nil
}

// validateSampling checks the optional sampling controls against the
// provider's documented ranges.
func validateSampling(temperature, topP *float64) error {
	if temperature != nil && (*temperature < 0 || *temperature > 1) {
		return fmt.Errorf("bedrock: temperature must be in [0, 1], got %v", *temperature)
	}
	if topP != nil && (*topP < 0 || *topP > 1) {
		return fmt.Errorf("bedrock: top P must be in [0, 1], got %v", *topP)
	}
	return nil
}

// ThinkingConfig requests reasoning from Claude models on Bedrock.
// Exactly one field is set; the zero config is invalid and New rejects
// it. Adaptive thinking (Claude Opus 4.6 and later, served through the
// Anthropic messages shape inside Converse) lets the model decide when
// and how much to think; older Claude models require a BudgetTokens
// budget instead. Thinking-by-default models need Disabled to turn
// thinking off, because omitting the thinking field selects adaptive
// thinking on them.
type ThinkingConfig struct {
	// Adaptive enables adaptive thinking.
	Adaptive bool
	// BudgetTokens bounds extended thinking on models that take a fixed
	// budget; it must be positive when set.
	BudgetTokens int
	// Disabled turns thinking off explicitly.
	Disabled bool
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
		return fmt.Errorf("bedrock: thinking config must set one of Adaptive, BudgetTokens, or Disabled")
	case set > 1:
		return fmt.Errorf("bedrock: thinking config must set exactly one of Adaptive, BudgetTokens, or Disabled")
	case t.BudgetTokens < 0:
		return fmt.Errorf("bedrock: thinking budget must not be negative, got %d", t.BudgetTokens)
	}
	return nil
}

// wireFields renders the validated config as the
// additionalModelRequestFields payload; nil keeps the field off the wire.
func (t *ThinkingConfig) wireFields() json.RawMessage {
	if t == nil {
		return nil
	}
	thinking := map[string]any{"type": "adaptive"}
	if t.BudgetTokens > 0 {
		thinking = map[string]any{"type": "enabled", "budget_tokens": t.BudgetTokens}
	} else if t.Disabled {
		thinking = map[string]any{"type": "disabled"}
	}
	encoded, err := json.Marshal(map[string]any{"thinking": thinking})
	if err != nil {
		return nil
	}
	return encoded
}
