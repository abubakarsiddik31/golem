package openai_test

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
	"github.com/abubakarsiddik31/golem/providers/openai"
)

// recordedServer replies with a scripted status and body and records the
// body and auth header of every request it receives.
type recordedServer struct {
	server *httptest.Server

	mu     sync.Mutex
	bodies []string
	auths  []string
}

func newRecordedServer(status int, body string) *recordedServer {
	recorder := &recordedServer{}
	recorder.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)
		recorder.mu.Lock()
		recorder.bodies = append(recorder.bodies, string(payload))
		recorder.auths = append(recorder.auths, r.Header.Get("Authorization"))
		recorder.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	return recorder
}

func (r *recordedServer) lastBody(t *testing.T) string {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.bodies) == 0 {
		t.Fatal("recordedServer received no requests")
	}
	return r.bodies[len(r.bodies)-1]
}

func (r *recordedServer) lastAuth(t *testing.T) string {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.auths) == 0 {
		t.Fatal("recordedServer received no requests")
	}
	return r.auths[len(r.auths)-1]
}

func newClient(t *testing.T, baseURL string) *openai.Client {
	t.Helper()
	client, err := openai.New(openai.Config{
		APIKey:  "test-key",
		BaseURL: baseURL,
		Model:   "gpt-4o",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return client
}

func userPrompt() model.Request {
	return model.Request{Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}}
}

func TestNewRejectsMissingRequiredConfiguration(t *testing.T) {
	t.Parallel()

	if _, err := openai.New(openai.Config{Model: "gpt-4o"}); err == nil {
		t.Fatal("New() error = nil, want missing API key rejection")
	}
	if _, err := openai.New(openai.Config{APIKey: "k"}); err == nil {
		t.Fatal("New() error = nil, want missing model rejection")
	}
}

func TestGenerateTranslatesRequestAndResponse(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{
		"choices": [{"message": {"role": "assistant", "content": "42"}}],
		"usage": {"prompt_tokens": 12, "completion_tokens": 1}
	}`)
	defer recorder.server.Close()

	client := newClient(t, recorder.server.URL)
	response, err := client.Generate(context.Background(), model.Request{
		Messages: []model.Message{
			{Role: model.RoleSystem, Content: "Be concise."},
			{Role: model.RoleUser, Content: "Answer?"},
		},
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if response.Message.Role != model.RoleAssistant || response.Message.Content != "42" {
		t.Fatalf("response message = %#v", response.Message)
	}
	if response.Usage != (model.Usage{InputTokens: 12, OutputTokens: 1}) {
		t.Fatalf("usage = %#v", response.Usage)
	}

	var sent struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		Tools []any `json:"tools"`
	}
	if err := json.Unmarshal([]byte(recorder.lastBody(t)), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}
	if sent.Model != "gpt-4o" {
		t.Fatalf("wire model = %q", sent.Model)
	}
	if len(sent.Messages) != 2 || sent.Messages[0].Role != "system" || sent.Messages[1].Role != "user" {
		t.Fatalf("wire messages = %#v", sent.Messages)
	}
	if sent.Tools != nil {
		t.Fatalf("wire tools = %#v, want omitted", sent.Tools)
	}
	if got := recorder.lastAuth(t); got != "Bearer test-key" {
		t.Fatalf("authorization = %q", got)
	}
}

func TestGenerateAdvertisesToolsAndNormalizesArguments(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{
		"choices": [{"message": {
			"role": "assistant",
			"content": "",
			"tool_calls": [
				{"id": "call-1", "type": "function",
				 "function": {"name": "roll_dice", "arguments": "{\"guess\":4}"}},
				{"id": "call-2", "type": "function",
				 "function": {"name": "spin", "arguments": ""}}
			]
		}}],
		"usage": {}
	}`)
	defer recorder.server.Close()

	client := newClient(t, recorder.server.URL)
	schema := json.RawMessage(`{"type":"object"}`)
	response, err := client.Generate(context.Background(), model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "roll"},
			{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
				{ID: "call-0", Name: "roll_dice", Args: json.RawMessage(`{"guess":1}`)},
			}},
			{Role: model.RoleTool, ToolCallID: "call-0", ToolName: "roll_dice", Content: "1"},
		},
		ToolSpecs: []model.ToolSpec{{Name: "roll_dice", Description: "Roll.", Schema: schema}},
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	var sent struct {
		Messages []struct {
			Role       string `json:"role"`
			Content    string `json:"content"`
			ToolCallID string `json:"tool_call_id"`
			ToolCalls  []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		}
		Tools []struct {
			Type     string `json:"type"`
			Function struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				Parameters  json.RawMessage `json:"parameters"`
			} `json:"function"`
		}
	}
	if err := json.Unmarshal([]byte(recorder.lastBody(t)), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}

	if len(sent.Tools) != 1 || sent.Tools[0].Type != "function" ||
		sent.Tools[0].Function.Name != "roll_dice" ||
		sent.Tools[0].Function.Description != "Roll." ||
		string(sent.Tools[0].Function.Parameters) != `{"type":"object"}` {
		t.Fatalf("wire tools = %#v", sent.Tools)
	}

	// Assistant tool calls carry stringified arguments.
	assistant := sent.Messages[1]
	if len(assistant.ToolCalls) != 1 ||
		assistant.ToolCalls[0].ID != "call-0" ||
		assistant.ToolCalls[0].Function.Name != "roll_dice" ||
		assistant.ToolCalls[0].Function.Arguments != `{"guess":1}` {
		t.Fatalf("wire assistant tool calls = %#v", assistant.ToolCalls)
	}

	// Tool results use the provider role and correlation ID.
	result := sent.Messages[2]
	if result.Role != "tool" || result.ToolCallID != "call-0" || result.Content != "1" {
		t.Fatalf("wire tool result = %#v", result)
	}

	// Stringified arguments come back as raw JSON; empty becomes {}.
	calls := response.Message.ToolCalls
	if len(calls) != 2 {
		t.Fatalf("tool calls = %#v", calls)
	}
	if calls[0].ID != "call-1" || calls[0].Name != "roll_dice" || string(calls[0].Args) != `{"guess":4}` {
		t.Fatalf("call-1 = %#v", calls[0])
	}
	if string(calls[1].Args) != "{}" {
		t.Fatalf("call-2 args = %s, want {} normalized from empty string", calls[1].Args)
	}
}

func TestGenerateClassifiesProviderErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		status    int
		body      string
		retryable bool
		wantCode  string
		wantMsg   string
	}{
		{
			name:      "unauthorized",
			status:    http.StatusUnauthorized,
			body:      `{"error": {"code": "invalid_api_key", "message": "Incorrect API key"}}`,
			retryable: false,
			wantCode:  "invalid_api_key",
			wantMsg:   "Incorrect API key",
		},
		{
			name:      "rate limited",
			status:    http.StatusTooManyRequests,
			body:      `{"error": {"message": "Rate limit reached"}}`,
			retryable: true,
			wantMsg:   "Rate limit reached",
		},
		{
			name:      "server error without body",
			status:    http.StatusInternalServerError,
			body:      ``,
			retryable: true,
			wantMsg:   "Internal Server Error",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			recorder := newRecordedServer(test.status, test.body)
			defer recorder.server.Close()

			_, err := newClient(t, recorder.server.URL).Generate(context.Background(), userPrompt())
			var apiError *openai.APIError
			if !errors.As(err, &apiError) {
				t.Fatalf("Generate() error = %v, want APIError", err)
			}
			if apiError.StatusCode != test.status {
				t.Fatalf("status = %d, want %d", apiError.StatusCode, test.status)
			}
			if apiError.Retryable() != test.retryable {
				t.Fatalf("retryable = %v, want %v", apiError.Retryable(), test.retryable)
			}
			if apiError.Code != test.wantCode {
				t.Fatalf("code = %q, want %q", apiError.Code, test.wantCode)
			}
			if apiError.Message != test.wantMsg {
				t.Fatalf("message = %q, want %q", apiError.Message, test.wantMsg)
			}
		})
	}
}

func TestGenerateRejectsMalformedAndEmptyResponses(t *testing.T) {
	t.Parallel()

	t.Run("malformed json", func(t *testing.T) {
		t.Parallel()
		recorder := newRecordedServer(http.StatusOK, `{`)
		defer recorder.server.Close()

		_, err := newClient(t, recorder.server.URL).Generate(context.Background(), userPrompt())
		var decodeError *openai.DecodeError
		if !errors.As(err, &decodeError) {
			t.Fatalf("Generate() error = %v, want DecodeError", err)
		}
	})

	t.Run("no choices", func(t *testing.T) {
		t.Parallel()
		recorder := newRecordedServer(http.StatusOK, `{"choices": [], "usage": {}}`)
		defer recorder.server.Close()

		_, err := newClient(t, recorder.server.URL).Generate(context.Background(), userPrompt())
		var decodeError *openai.DecodeError
		if !errors.As(err, &decodeError) || !strings.Contains(err.Error(), "no choices") {
			t.Fatalf("Generate() error = %v, want DecodeError about choices", err)
		}
	})
}

func TestGenerateClassifiesTransportFailures(t *testing.T) {
	t.Parallel()

	t.Run("connection refused is retryable", func(t *testing.T) {
		t.Parallel()
		recorder := newRecordedServer(http.StatusOK, `{}`)
		baseURL := recorder.server.URL
		recorder.server.Close()

		_, err := newClient(t, baseURL).Generate(context.Background(), userPrompt())
		var transportError *openai.TransportError
		if !errors.As(err, &transportError) {
			t.Fatalf("Generate() error = %v, want TransportError", err)
		}
		if !transportError.Retryable() {
			t.Fatal("TransportError.Retryable() = false, want true")
		}
		if !model.IsRetryable(err) {
			t.Fatal("model.IsRetryable(err) = false, want true")
		}
	})
}

func TestGeneratePropagatesCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	recorder := newRecordedServer(http.StatusOK, `{"choices": [{"message": {"content": "late"}}]}`)
	defer recorder.server.Close()

	_, err := newClient(t, recorder.server.URL).Generate(ctx, userPrompt())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Generate() error = %v, want context.Canceled", err)
	}
	var transportError *openai.TransportError
	if !errors.As(err, &transportError) {
		t.Fatalf("Generate() error = %v, want TransportError", err)
	}
	if transportError.Retryable() {
		t.Fatal("TransportError.Retryable() = true, want false for cancellation")
	}
	if model.IsRetryable(err) {
		t.Fatal("model.IsRetryable(err) = true, want false for cancellation")
	}
}

func TestGenerateMapsOutputSchemaToResponseFormat(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{
		"choices": [{"message": {"role": "assistant", "content": "{\"city\":\"Lagos\"}"}}]
	}`)
	defer recorder.server.Close()
	client := newClient(t, recorder.server.URL)

	schema := json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"],"additionalProperties":false}`)
	if _, err := client.Generate(context.Background(), model.Request{
		Messages:     []model.Message{{Role: model.RoleUser, Content: "name a city"}},
		OutputSchema: schema,
	}); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	var sent struct {
		ResponseFormat *struct {
			Type       string `json:"type"`
			JSONSchema struct {
				Name   string          `json:"name"`
				Strict bool            `json:"strict"`
				Schema json.RawMessage `json:"schema"`
			} `json:"json_schema"`
		} `json:"response_format"`
	}
	if err := json.Unmarshal([]byte(recorder.lastBody(t)), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}
	if sent.ResponseFormat == nil {
		t.Fatal("response_format = nil, want json_schema")
	}
	if sent.ResponseFormat.Type != "json_schema" {
		t.Fatalf("response_format.type = %q, want json_schema", sent.ResponseFormat.Type)
	}
	if name, strict := sent.ResponseFormat.JSONSchema.Name, sent.ResponseFormat.JSONSchema.Strict; name != "output" || !strict {
		t.Fatalf("json_schema = {name: %q, strict: %v}, want {output, true}", name, strict)
	}
	if string(sent.ResponseFormat.JSONSchema.Schema) != string(schema) {
		t.Fatalf("json_schema.schema = %s, want the request schema", sent.ResponseFormat.JSONSchema.Schema)
	}

	// Without a schema the field stays off the wire entirely.
	if _, err := client.Generate(context.Background(), userPrompt()); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	sent.ResponseFormat = nil
	if err := json.Unmarshal([]byte(recorder.lastBody(t)), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}
	if sent.ResponseFormat != nil {
		t.Fatalf("response_format = %#v, want nil without an output schema", sent.ResponseFormat)
	}
}

func TestGenerateStreamCarriesResponseFormat(t *testing.T) {
	t.Parallel()

	server := newSSEServer(t,
		"data: {\"choices\":[{\"delta\":{\"content\":\"{}\"}}]}\n\n",
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2}}\n\n",
		"data: [DONE]\n\n",
	)
	client := newClient(t, server.server.URL)

	if _, err := client.GenerateStream(context.Background(), model.Request{
		Messages:     []model.Message{{Role: model.RoleUser, Content: "name a city"}},
		OutputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(model.Delta) error { return nil }); err != nil {
		t.Fatalf("GenerateStream() error = %v", err)
	}

	var sent struct {
		ResponseFormat *struct {
			Type string `json:"type"`
		} `json:"response_format"`
	}
	if err := json.Unmarshal([]byte(server.lastBody(t)), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}
	if sent.ResponseFormat == nil || sent.ResponseFormat.Type != "json_schema" {
		t.Fatalf("response_format = %#v, want json_schema", sent.ResponseFormat)
	}
}

func TestGenerateMapsPromptPartsToContentArray(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{
		"choices": [{"message": {"role": "assistant", "content": "a red square"}}]
	}`)
	defer recorder.server.Close()
	client := newClient(t, recorder.server.URL)

	if _, err := client.Generate(context.Background(), model.Request{Messages: []model.Message{
		{Role: model.RoleSystem, Content: "Be concise."},
		{
			Role:    model.RoleUser,
			Content: "what is this",
			Parts: []model.Part{
				model.ImageURL("https://example.com/a.png"),
				model.ImageData("image/png", []byte{1, 2}),
			},
		},
	}}); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	var sent struct {
		Messages []struct {
			Role    string            `json:"role"`
			Content json.RawMessage   `json:"content"`
			Parts   []json.RawMessage `json:"parts"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(recorder.lastBody(t)), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}
	if string(sent.Messages[0].Content) != `"Be concise."` {
		t.Fatalf("system content = %s, want a plain string", sent.Messages[0].Content)
	}
	if sent.Messages[1].Parts != nil {
		t.Fatalf("wire message leaked parts field: %s", sent.Messages[1].Parts)
	}

	var content []any
	if err := json.Unmarshal(sent.Messages[1].Content, &content); err != nil {
		t.Fatalf("user content is not an array: %s", sent.Messages[1].Content)
	}
	wantParts := []string{
		`{"type":"text","text":"what is this"}`,
		`{"type":"image_url","image_url":{"url":"https://example.com/a.png"}}`,
		`{"type":"image_url","image_url":{"url":"data:image/png;base64,AQI="}}`,
	}
	if len(content) != len(wantParts) {
		t.Fatalf("content array length = %d, want %d: %s", len(content), len(wantParts), sent.Messages[1].Content)
	}
	for i, want := range wantParts {
		got, err := json.Marshal(content[i])
		if err != nil {
			t.Fatal(err)
		}
		var gotValue, wantValue any
		if err := json.Unmarshal(got, &gotValue); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(gotValue, wantValue) {
			t.Fatalf("content[%d] = %s, want %s", i, got, want)
		}
	}
}

func TestConfiguredSamplingControlsOnWire(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, "{}")
	temperature, topP := 0.4, 0.9
	client, err := openai.New(openai.Config{
		APIKey:      "test-key",
		BaseURL:     recorder.server.URL,
		Model:       "gpt-4o",
		Temperature: &temperature,
		TopP:        &topP,
		MaxTokens:   256,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	// The response body is irrelevant here; the request is what is asserted.
	_, _ = client.Generate(context.Background(), model.Request{Messages: []model.Message{
		{Role: model.RoleUser, Content: "hi"},
	}})

	var sent map[string]any
	if err := json.Unmarshal([]byte(recorder.lastBody(t)), &sent); err != nil {
		t.Fatalf("decode sent body: %v", err)
	}
	if sent["temperature"] != 0.4 || sent["top_p"] != 0.9 || sent["max_tokens"] != float64(256) {
		t.Fatalf("sampling controls = %v", sent)
	}

	// A default config keeps the controls off the wire entirely.
	defaultClient := newClient(t, recorder.server.URL)
	_, _ = defaultClient.Generate(context.Background(), model.Request{Messages: []model.Message{
		{Role: model.RoleUser, Content: "hi"},
	}})
	var plain map[string]any
	if err := json.Unmarshal([]byte(recorder.lastBody(t)), &plain); err != nil {
		t.Fatalf("decode sent body: %v", err)
	}
	for _, key := range []string{"temperature", "top_p", "max_tokens"} {
		if _, present := plain[key]; present {
			t.Fatalf("unset %q must stay off the wire, got %v", key, plain[key])
		}
	}
}

func TestNewRejectsInvalidSamplingControls(t *testing.T) {
	t.Parallel()

	invalid := func(v float64) *float64 { return &v }
	for name, cfg := range map[string]openai.Config{
		"negative temperature": {APIKey: "k", Model: "m", Temperature: invalid(-0.1)},
		"hot temperature":      {APIKey: "k", Model: "m", Temperature: invalid(2.1)},
		"top P above one":      {APIKey: "k", Model: "m", TopP: invalid(1.1)},
		"negative max tokens":  {APIKey: "k", Model: "m", MaxTokens: -1},
	} {
		if _, err := openai.New(cfg); err == nil {
			t.Fatalf("%s: New() = nil error, want rejection", name)
		}
	}
}

func TestGenerateCapturesReasoningContent(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{"choices":[{"message":{
		"role":"assistant",
		"content":"The answer is 4.",
		"reasoning_content":"adding 2 and 2"
	}}],"usage":{"prompt_tokens":8,"completion_tokens":4}}`)
	client, err := openai.New(openai.Config{
		APIKey:          "test-key",
		BaseURL:         recorder.server.URL,
		Model:           "gpt-5.2",
		ReasoningEffort: "low",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	response, err := client.Generate(context.Background(), model.Request{Messages: []model.Message{
		{Role: model.RoleUser, Content: "hi"},
	}})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	thinking := response.Message.Thinking
	if len(thinking) != 1 || thinking[0].Text != "adding 2 and 2" {
		t.Fatalf("thinking blocks = %#v", thinking)
	}
	if response.Message.Content != "The answer is 4." {
		t.Fatalf("content = %q", response.Message.Content)
	}

	// The effort control rides the request; captured reasoning never does:
	// chat-completions endpoints disagree on replay, from ignoring it to
	// rejecting it.
	request := model.Request{Messages: []model.Message{
		{Role: model.RoleUser, Content: "hi"},
		response.Message,
	}}
	if _, err := client.Generate(context.Background(), request); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	body := recorder.lastBody(t)
	if !strings.Contains(body, `"reasoning_effort":"low"`) {
		t.Fatalf("wire body = %s, want reasoning_effort", body)
	}
	assistant := body[strings.Index(body, `"role":"assistant"`):]
	if strings.Contains(assistant[:min(len(assistant), 400)], `"reasoning_content"`) {
		t.Fatalf("captured reasoning was replayed on the wire: %s", body)
	}

	// A default config keeps the effort control off the wire.
	plain := newClient(t, recorder.server.URL)
	if _, err := plain.Generate(context.Background(), model.Request{Messages: []model.Message{
		{Role: model.RoleUser, Content: "hi"},
	}}); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if strings.Contains(recorder.lastBody(t), "reasoning_effort") {
		t.Fatalf("default config put reasoning_effort on the wire: %s", recorder.lastBody(t))
	}
}
