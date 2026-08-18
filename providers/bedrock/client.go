// Package bedrock adapts the AWS Bedrock Runtime Converse API to Golem's
// provider-neutral model contract.
//
// The package is stdlib-only transport, including AWS Signature Version 4
// request signing, explicit configuration, typed error classification,
// and normalization of provider encodings into the model contract.
// Streaming is not implemented yet: ConverseStream uses AWS binary
// event-stream framing rather than SSE.
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
	httpRequest, err := c.newConverseHTTPRequest(ctx, request)
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
// converse endpoint. A request carrying an output schema maps to the
// json_schema output format.
func (c *Client) newConverseHTTPRequest(ctx context.Context, request model.Request) (*http.Request, error) {
	system, turns, err := toWireMessages(request.Messages)
	if err != nil {
		return nil, err
	}
	var inferenceConfig *wireInference
	if c.cfg.MaxTokens > 0 {
		inferenceConfig = &wireInference{MaxTokens: c.cfg.MaxTokens}
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
	body, err := json.Marshal(converseRequest{
		Messages:        turns,
		System:          system,
		ToolConfig:      toWireToolConfig(request.ToolSpecs),
		InferenceConfig: inferenceConfig,
		OutputConfig:    outputConfig,
	})
	if err != nil {
		return nil, &DecodeError{Stage: "encode request", Err: err}
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.cfg.BaseURL+"/model/"+c.cfg.Model+"/converse",
		bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("bedrock: build request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	signV4(httpRequest, body, c.cfg.Credentials, c.cfg.Region, serviceName, time.Now())
	return httpRequest, nil
}
