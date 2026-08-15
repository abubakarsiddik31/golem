package openai_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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
			if apiError.Retryable != test.retryable {
				t.Fatalf("retryable = %v, want %v", apiError.Retryable, test.retryable)
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
}
