package bedrock_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/bedrock"
)

// recordedServer replies with a scripted status, body, and headers, and
// records the path, body, and auth headers of every request it receives.
type recordedServer struct {
	server *httptest.Server

	mu          sync.Mutex
	bodies      []string
	paths       []string
	authorizers []string
	amzDates    []string
}

func newRecordedServer(status int, body string, responseHeaders map[string]string) *recordedServer {
	recorder := &recordedServer{}
	recorder.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)
		recorder.mu.Lock()
		recorder.bodies = append(recorder.bodies, string(payload))
		recorder.paths = append(recorder.paths, r.URL.Path)
		recorder.authorizers = append(recorder.authorizers, r.Header.Get("Authorization"))
		recorder.amzDates = append(recorder.amzDates, r.Header.Get("x-amz-date"))
		recorder.mu.Unlock()
		for name, value := range responseHeaders {
			w.Header().Set(name, value)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	return recorder
}

// lastOptional returns the last recorded body, or "" when the server
// received no request.
func (r *recordedServer) lastOptional() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.bodies) == 0 {
		return ""
	}
	return r.bodies[len(r.bodies)-1]
}

func (r *recordedServer) last(t *testing.T) recordedRequest {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.bodies) == 0 {
		t.Fatal("recordedServer received no requests")
	}
	return recordedRequest{
		body:       r.bodies[len(r.bodies)-1],
		path:       r.paths[len(r.paths)-1],
		authorizer: r.authorizers[len(r.authorizers)-1],
		amzDate:    r.amzDates[len(r.amzDates)-1],
	}
}

type recordedRequest struct {
	body       string
	path       string
	authorizer string
	amzDate    string
}

func newClient(t *testing.T, baseURL string) *bedrock.Client {
	t.Helper()
	client, err := bedrock.New(bedrock.Config{
		Credentials: bedrock.Credentials{
			AccessKeyID:     "AKIDEXAMPLE",
			SecretAccessKey: "secret",
		},
		Region:  "us-east-1",
		Model:   "anthropic.claude-sonnet-4-5-20250929-v1:0",
		BaseURL: baseURL,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return client
}

func TestNewRejectsMissingRequiredConfiguration(t *testing.T) {
	t.Parallel()

	base := bedrock.Config{
		Credentials: bedrock.Credentials{AccessKeyID: "a", SecretAccessKey: "s"},
		Region:      "us-east-1",
		Model:       "m",
	}
	tests := []struct {
		name string
		cfg  bedrock.Config
	}{
		{"missing access key", bedrock.Config{Credentials: bedrock.Credentials{SecretAccessKey: "s"}, Region: base.Region, Model: base.Model}},
		{"missing secret key", bedrock.Config{Credentials: bedrock.Credentials{AccessKeyID: "a"}, Region: base.Region, Model: base.Model}},
		{"missing region", bedrock.Config{Credentials: base.Credentials, Model: base.Model}},
		{"missing model", bedrock.Config{Credentials: base.Credentials, Region: base.Region}},
		{"negative max tokens", bedrock.Config{Credentials: base.Credentials, Region: base.Region, Model: base.Model, MaxTokens: -1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := bedrock.New(test.cfg); err == nil {
				t.Fatal("New() error = nil, want rejection")
			}
		})
	}
}

func TestGenerateSignsAndTranslatesRequest(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{
		"output": {"message": {"role": "assistant", "content": [
			{"text": "Rolling."},
			{"toolUse": {"toolUseId": "toolu-1", "name": "roll", "input": {"n":2}}}
		]}},
		"stopReason": "tool_use",
		"usage": {"inputTokens": 12, "outputTokens": 3}
	}`, nil)
	defer recorder.server.Close()

	client := newClient(t, recorder.server.URL)
	response, err := client.Generate(context.Background(), model.Request{
		Messages: []model.Message{
			{Role: model.RoleSystem, Content: "Be concise."},
			{Role: model.RoleUser, Content: "Roll a die."},
			{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
				{ID: "toolu-1", Name: "roll", Args: json.RawMessage(`{"n":1}`)},
			}},
			{Role: model.RoleTool, ToolCallID: "toolu-1", ToolName: "roll", Content: "rolled 1"},
			{Role: model.RoleUser, Content: "Now roll two."},
		},
		ToolSpecs: []model.ToolSpec{{
			Name:        "roll",
			Description: "Roll a die.",
			Schema:      json.RawMessage(`{"type":"object","properties":{"n":{"type":"integer"}}}`),
		}},
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	if response.Message.Content != "Rolling." || len(response.Message.ToolCalls) != 1 {
		t.Fatalf("response message = %#v", response.Message)
	}
	call := response.Message.ToolCalls[0]
	if call.ID != "toolu-1" || call.Name != "roll" || string(call.Args) != `{"n":2}` {
		t.Fatalf("tool call = %#v", call)
	}
	if response.Usage != (model.Usage{InputTokens: 12, OutputTokens: 3}) {
		t.Fatalf("usage = %#v", response.Usage)
	}

	sent := recorder.last(t)
	if !strings.HasSuffix(sent.path, "/model/anthropic.claude-sonnet-4-5-20250929-v1:0/converse") {
		t.Fatalf("path = %q, want the converse endpoint", sent.path)
	}
	if !strings.HasPrefix(sent.authorizer, "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/") ||
		!strings.Contains(sent.authorizer, "/us-east-1/bedrock/aws4_request") {
		t.Fatalf("Authorization = %q, want a SigV4 bedrock scope", sent.authorizer)
	}
	if sent.amzDate == "" {
		t.Fatal("x-amz-date header missing")
	}

	var request struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Text    string `json:"text,omitempty"`
				ToolUse *struct {
					ToolUseID string          `json:"toolUseId"`
					Name      string          `json:"name"`
					Input     json.RawMessage `json:"input"`
				} `json:"toolUse,omitempty"`
				ToolResult *struct {
					ToolUseID string `json:"toolUseId"`
					Content   []struct {
						Text string `json:"text"`
					} `json:"content"`
				} `json:"toolResult,omitempty"`
			} `json:"content"`
		} `json:"messages"`
		System []struct {
			Text string `json:"text"`
		} `json:"system"`
		ToolConfig *struct {
			Tools []struct {
				ToolSpec struct {
					Name        string `json:"name"`
					InputSchema struct {
						JSON json.RawMessage `json:"json"`
					} `json:"inputSchema"`
				} `json:"toolSpec"`
			} `json:"tools"`
		} `json:"toolConfig"`
		InferenceConfig json.RawMessage `json:"inferenceConfig"`
	}
	if err := json.Unmarshal([]byte(sent.body), &request); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}
	if len(request.System) != 1 || request.System[0].Text != "Be concise." {
		t.Fatalf("system = %#v, want top-level guidance", request.System)
	}
	if request.ToolConfig == nil || len(request.ToolConfig.Tools) != 1 ||
		request.ToolConfig.Tools[0].ToolSpec.Name != "roll" ||
		string(request.ToolConfig.Tools[0].ToolSpec.InputSchema.JSON) != `{"type":"object","properties":{"n":{"type":"integer"}}}` {
		t.Fatalf("toolConfig = %#v", request.ToolConfig)
	}
	// Tool results and the following user prompt merge into one user turn.
	if len(request.Messages) != 3 {
		t.Fatalf("messages = %#v, want 3 turns", request.Messages)
	}
	third := request.Messages[2]
	if third.Role != "user" || len(third.Content) != 2 ||
		third.Content[0].ToolResult == nil || third.Content[0].ToolResult.ToolUseID != "toolu-1" ||
		third.Content[0].ToolResult.Content[0].Text != "rolled 1" ||
		third.Content[1].Text != "Now roll two." {
		t.Fatalf("merged user turn = %#v", third)
	}
	// MaxTokens unset: no inferenceConfig on the wire.
	if string(request.InferenceConfig) != "" && string(request.InferenceConfig) != "null" {
		t.Fatalf("inferenceConfig = %s, want absent without MaxTokens", request.InferenceConfig)
	}
}

func TestGenerateMapsOutputSchemaToOutputConfig(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{
		"output": {"message": {"role": "assistant", "content": [{"text": "{\"city\":\"Lagos\"}"}]}},
		"usage": {"inputTokens": 5, "outputTokens": 2}
	}`, nil)
	defer recorder.server.Close()
	client := newClient(t, recorder.server.URL)

	schema := json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`)
	if _, err := client.Generate(context.Background(), model.Request{
		Messages:     []model.Message{{Role: model.RoleUser, Content: "name a city"}},
		OutputSchema: schema,
	}); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	var sent struct {
		OutputConfig *struct {
			TextFormat struct {
				Type      string `json:"type"`
				Structure struct {
					JSONSchema struct {
						Name   string `json:"name"`
						Schema string `json:"schema"`
					} `json:"jsonSchema"`
				} `json:"structure"`
			} `json:"textFormat"`
		} `json:"outputConfig"`
	}
	if err := json.Unmarshal([]byte(recorder.last(t).body), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}
	if sent.OutputConfig == nil || sent.OutputConfig.TextFormat.Type != "json_schema" {
		t.Fatalf("outputConfig = %#v, want json_schema", sent.OutputConfig)
	}
	def := sent.OutputConfig.TextFormat.Structure.JSONSchema
	if def.Name != "output" || def.Schema != string(schema) {
		t.Fatalf("jsonSchema = {%q %q}, want the schema as a string", def.Name, def.Schema)
	}
}

func TestGenerateClassifiesProviderErrors(t *testing.T) {
	t.Parallel()

	t.Run("retryable on 429 ThrottlingException", func(t *testing.T) {
		t.Parallel()
		recorder := newRecordedServer(429, `{"message": "Too many requests"}`,
			map[string]string{"x-amzn-errortype": "ThrottlingException"})
		defer recorder.server.Close()

		_, err := newClient(t, recorder.server.URL).Generate(context.Background(), model.Request{
			Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
		})
		var apiError *bedrock.APIError
		if !errors.As(err, &apiError) {
			t.Fatalf("Generate() error = %v, want APIError", err)
		}
		if apiError.Code != "ThrottlingException" || apiError.Message != "Too many requests" {
			t.Fatalf("APIError = {%q %q}", apiError.Code, apiError.Message)
		}
		if !model.IsRetryable(err) {
			t.Fatal("model.IsRetryable(err) = false, want true for 429")
		}
	})

	t.Run("not retryable on 400 ValidationException", func(t *testing.T) {
		t.Parallel()
		recorder := newRecordedServer(400, `{"message": "Invalid request"}`,
			map[string]string{"x-amzn-errortype": "ValidationException:http://internal"})
		defer recorder.server.Close()

		_, err := newClient(t, recorder.server.URL).Generate(context.Background(), model.Request{
			Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
		})
		var apiError *bedrock.APIError
		if !errors.As(err, &apiError) {
			t.Fatalf("Generate() error = %v, want APIError", err)
		}
		if apiError.Code != "ValidationException" {
			t.Fatalf("APIError code = %q, want the header type without the suffix", apiError.Code)
		}
		if model.IsRetryable(err) {
			t.Fatal("model.IsRetryable(err) = true, want false for 400")
		}
	})
}

func TestGeneratePropagatesCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	recorder := newRecordedServer(http.StatusOK, `{"output": {"message": {"content": [{"text": "late"}]}}}`, nil)
	defer recorder.server.Close()

	_, err := newClient(t, recorder.server.URL).Generate(ctx, model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Generate() error = %v, want context.Canceled", err)
	}
	var transportError *bedrock.TransportError
	if !errors.As(err, &transportError) {
		t.Fatalf("Generate() error = %v, want TransportError", err)
	}
	if model.IsRetryable(err) {
		t.Fatal("model.IsRetryable(err) = true, want false for cancellation")
	}
}

func TestGenerateMapsInlineImagePartsToImageBlocks(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{
		"output": {"message": {"role": "assistant", "content": [{"text": "a red square"}]}},
		"usage": {"inputTokens": 10, "outputTokens": 4}
	}`, nil)
	defer recorder.server.Close()
	client := newClient(t, recorder.server.URL)

	if _, err := client.Generate(context.Background(), model.Request{Messages: []model.Message{{
		Role:    model.RoleUser,
		Content: "what is this",
		Parts:   []model.Part{model.ImageData("image/png", []byte{1, 2})},
	}}}); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	var sent struct {
		Messages []struct {
			Role    string           `json:"role"`
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(recorder.last(t).body), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}
	wantBlocks := []map[string]any{
		{"text": "what is this"},
		{"image": map[string]any{"format": "png", "source": map[string]any{"bytes": "AQI="}}},
	}
	got := sent.Messages[0].Content
	if len(got) != len(wantBlocks) {
		t.Fatalf("user turn blocks = %#v, want %d blocks", got, len(wantBlocks))
	}
	for i, want := range wantBlocks {
		if !reflect.DeepEqual(got[i], want) {
			t.Fatalf("block[%d] = %#v, want %#v", i, got[i], want)
		}
	}
}

func TestGenerateRejectsURLImagePartsWithoutSending(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{"output": {"message": {"role": "assistant", "content": [{"text": "unused"}]}}}`, nil)
	defer recorder.server.Close()
	client := newClient(t, recorder.server.URL)

	_, err := client.Generate(context.Background(), model.Request{Messages: []model.Message{{
		Role:    model.RoleUser,
		Content: "what is this",
		Parts:   []model.Part{model.ImageURL("https://example.com/a.png")},
	}}})
	if !errors.Is(err, bedrock.ErrUnsupportedContent) {
		t.Fatalf("Generate() error = %v, want ErrUnsupportedContent", err)
	}
	if !strings.Contains(err.Error(), "inline") {
		t.Fatalf("error should point at inline data as the fix: %v", err)
	}
	if request := recorder.lastOptional(); request != "" {
		t.Fatalf("adapter sent a request despite rejecting the content: %s", request)
	}
}

func TestConfiguredSamplingControlsOnWire(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{}`, nil)
	temperature, topP := 0.4, 0.9
	client, err := bedrock.New(bedrock.Config{
		Credentials: bedrock.Credentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret"},
		Region:      "us-east-1",
		Model:       "anthropic.claude-sonnet-4-5-20250929-v1:0",
		BaseURL:     recorder.server.URL,
		Temperature: &temperature,
		TopP:        &topP,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	// The response body is irrelevant here; the request is what is asserted.
	_, _ = client.Generate(context.Background(), model.Request{Messages: []model.Message{
		{Role: model.RoleUser, Content: "hi"},
	}})

	var sent struct {
		InferenceConfig *struct {
			Temperature *float64 `json:"temperature"`
			TopP        *float64 `json:"topP"`
		} `json:"inferenceConfig"`
	}
	if err := json.Unmarshal([]byte(recorder.last(t).body), &sent); err != nil {
		t.Fatalf("decode sent body: %v", err)
	}
	if sent.InferenceConfig == nil ||
		sent.InferenceConfig.Temperature == nil || *sent.InferenceConfig.Temperature != 0.4 ||
		sent.InferenceConfig.TopP == nil || *sent.InferenceConfig.TopP != 0.9 {
		t.Fatalf("inference config = %+v", sent.InferenceConfig)
	}

	// A default config keeps inferenceConfig off the wire entirely.
	var plain map[string]any
	defaultClient := newClient(t, recorder.server.URL)
	_, _ = defaultClient.Generate(context.Background(), model.Request{Messages: []model.Message{
		{Role: model.RoleUser, Content: "hi"},
	}})
	if err := json.Unmarshal([]byte(recorder.last(t).body), &plain); err != nil {
		t.Fatalf("decode sent body: %v", err)
	}
	if _, present := plain["inferenceConfig"]; present {
		t.Fatalf("unset inferenceConfig must stay off the wire, got %v", plain["inferenceConfig"])
	}
}

func TestNewRejectsInvalidSamplingControls(t *testing.T) {
	t.Parallel()

	invalid := func(v float64) *float64 { return &v }
	base := bedrock.Config{
		Credentials: bedrock.Credentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret"},
		Region:      "us-east-1",
		Model:       "m",
	}
	for name, cfg := range map[string]bedrock.Config{
		"negative temperature": {Credentials: base.Credentials, Region: base.Region, Model: base.Model, Temperature: invalid(-0.1)},
		"hot temperature":      {Credentials: base.Credentials, Region: base.Region, Model: base.Model, Temperature: invalid(1.1)},
		"top P above one":      {Credentials: base.Credentials, Region: base.Region, Model: base.Model, TopP: invalid(1.1)},
	} {
		if _, err := bedrock.New(cfg); err == nil {
			t.Fatalf("%s: New() = nil error, want rejection", name)
		}
	}
}

func TestThinkingConfigMapsToConverseFields(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		thinking     *bedrock.ThinkingConfig
		effort       string
		wantThinking string // substring expected in additionalModelRequestFields
		wantEffort   bool
	}{
		{"adaptive", &bedrock.ThinkingConfig{Adaptive: true}, "", `"thinking":{"type":"adaptive"}`, false},
		{"budget", &bedrock.ThinkingConfig{BudgetTokens: 4096}, "", `"budget_tokens":4096`, false},
		{"disabled", &bedrock.ThinkingConfig{Disabled: true}, "", `"type":"disabled"`, false},
		{"adaptive with effort", &bedrock.ThinkingConfig{Adaptive: true}, "high", `"type":"adaptive"`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			recorder := newRecordedServer(http.StatusOK,
				`{"output":{"message":{"role":"assistant","content":[{"text":"ok"}]}},"stopReason":"end_turn","usage":{"inputTokens":1,"outputTokens":1}}`,
				nil)
			client, err := bedrock.New(bedrock.Config{
				Credentials: bedrock.Credentials{AccessKeyID: "a", SecretAccessKey: "s"},
				Region:      "us-east-1",
				Model:       "anthropic.claude-sonnet-4-5-20250929-v1:0",
				BaseURL:     recorder.server.URL,
				Thinking:    tc.thinking,
				Effort:      tc.effort,
			})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if _, err := client.Generate(context.Background(), model.Request{Messages: []model.Message{
				{Role: model.RoleUser, Content: "hi"},
			}}); err != nil {
				t.Fatalf("Generate() error = %v", err)
			}

			body := recorder.last(t).body
			var sent struct {
				AdditionalModelRequestFields *json.RawMessage `json:"additionalModelRequestFields"`
				OutputConfig                 *struct {
					Effort string `json:"effort"`
				} `json:"outputConfig"`
			}
			if err := json.Unmarshal([]byte(body), &sent); err != nil {
				t.Fatalf("decode sent body: %v", err)
			}
			if tc.wantThinking == "" {
				if sent.AdditionalModelRequestFields != nil {
					t.Fatalf("additionalModelRequestFields = %s, want absent", *sent.AdditionalModelRequestFields)
				}
			} else {
				if sent.AdditionalModelRequestFields == nil || !strings.Contains(string(*sent.AdditionalModelRequestFields), tc.wantThinking) {
					t.Fatalf("additionalModelRequestFields = %v, want containing %s", sent.AdditionalModelRequestFields, tc.wantThinking)
				}
			}
			if tc.wantEffort && (sent.OutputConfig == nil || sent.OutputConfig.Effort != "high") {
				t.Fatalf("outputConfig effort missing: %v", sent.OutputConfig)
			}
			if !tc.wantEffort && sent.OutputConfig != nil {
				t.Fatalf("outputConfig = %v, want absent", sent.OutputConfig)
			}
		})
	}

	// A default config keeps both off the wire.
	recorder := newRecordedServer(http.StatusOK,
		`{"output":{"message":{"role":"assistant","content":[{"text":"ok"}]}},"stopReason":"end_turn","usage":{"inputTokens":1,"outputTokens":1}}`, nil)
	client := newClient(t, recorder.server.URL)
	if _, err := client.Generate(context.Background(), model.Request{Messages: []model.Message{
		{Role: model.RoleUser, Content: "hi"},
	}}); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	body := recorder.last(t).body
	if strings.Contains(body, "additionalModelRequestFields") || strings.Contains(body, `"outputConfig"`) {
		t.Fatalf("default config put thinking controls on the wire: %s", body)
	}
}

func TestNewRejectsInvalidThinkingConfig(t *testing.T) {
	t.Parallel()

	base := bedrock.Config{
		Credentials: bedrock.Credentials{AccessKeyID: "a", SecretAccessKey: "s"},
		Region:      "us-east-1",
		Model:       "m",
	}
	for name, thinking := range map[string]*bedrock.ThinkingConfig{
		"empty":     {},
		"conflict":  {Adaptive: true, Disabled: true},
		"negative":  {BudgetTokens: -1},
		"two modes": {Adaptive: true, BudgetTokens: 100},
	} {
		cfg := base
		cfg.Thinking = thinking
		if _, err := bedrock.New(cfg); err == nil {
			t.Fatalf("%s config: New() error = nil, want rejection", name)
		}
	}
}

func TestGenerateNormalizesReasoningContent(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{"output":{"message":{"role":"assistant","content":[
		{"reasoningContent":{"reasoningText":{"text":"step by step","signature":"sig-1"}}},
		{"reasoningContent":{"redactedContent":"enc"}},
		{"text":"The answer is 4."},
		{"toolUse":{"toolUseId":"call-1","name":"calc","input":{"op":"add"}}}
	]}},"stopReason":"tool_use","usage":{"inputTokens":10,"outputTokens":5}}`, nil)
	client := newClient(t, recorder.server.URL)

	response, err := client.Generate(context.Background(), model.Request{Messages: []model.Message{
		{Role: model.RoleUser, Content: "hi"},
	}})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	thinking := response.Message.Thinking
	if len(thinking) != 2 || thinking[0].Text != "step by step" || thinking[0].Signature != "sig-1" ||
		thinking[1].Redacted != "enc" {
		t.Fatalf("thinking blocks = %#v", thinking)
	}
	if response.Message.Content != "The answer is 4." || len(response.Message.ToolCalls) != 1 {
		t.Fatalf("message = %#v", response.Message)
	}

	// History carrying reasoning replays the blocks before text and calls.
	request := model.Request{Messages: []model.Message{
		{Role: model.RoleUser, Content: "hi"},
		{Role: model.RoleAssistant, Content: "4", Thinking: thinking,
			ToolCalls: []model.ToolCall{{ID: "call-1", Name: "calc", Args: json.RawMessage(`{}`)}}},
	}}
	if _, err := client.Generate(context.Background(), request); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	var sent struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Text             string `json:"text"`
				ReasoningContent *struct {
					ReasoningText *struct {
						Text      string `json:"text"`
						Signature string `json:"signature"`
					} `json:"reasoningText"`
					RedactedContent string `json:"redactedContent"`
				} `json:"reasoningContent"`
				ToolUse *struct {
					ToolUseID string `json:"toolUseId"`
				} `json:"toolUse"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(recorder.last(t).body), &sent); err != nil {
		t.Fatalf("decode sent body: %v", err)
	}
	blocks := sent.Messages[1].Content
	if len(blocks) != 4 || blocks[0].ReasoningContent == nil || blocks[1].ReasoningContent == nil ||
		blocks[2].Text != "4" || blocks[3].ToolUse == nil {
		t.Fatalf("assistant blocks = %#v, want reasoning, redacted, text, toolUse", blocks)
	}
	if blocks[0].ReasoningContent.ReasoningText.Text != "step by step" ||
		blocks[0].ReasoningContent.ReasoningText.Signature != "sig-1" ||
		blocks[1].ReasoningContent.RedactedContent != "enc" {
		t.Fatalf("replayed reasoning = %#v, want signatures preserved", blocks[:2])
	}
}
