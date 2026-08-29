package anthropic_test

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
	"github.com/abubakarsiddik31/golem/providers/anthropic"
)

// recordedServer replies with a scripted status and body and records the
// body and headers of every request it receives.
type recordedServer struct {
	server *httptest.Server

	mu       sync.Mutex
	bodies   []string
	apiKeys  []string
	versions []string
}

func newRecordedServer(status int, body string) *recordedServer {
	recorder := &recordedServer{}
	recorder.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)
		recorder.mu.Lock()
		recorder.bodies = append(recorder.bodies, string(payload))
		recorder.apiKeys = append(recorder.apiKeys, r.Header.Get("x-api-key"))
		recorder.versions = append(recorder.versions, r.Header.Get("anthropic-version"))
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

func (r *recordedServer) lastHeader(t *testing.T, name string) string {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	values := map[string][]string{"x-api-key": r.apiKeys, "anthropic-version": r.versions}
	if len(values[name]) == 0 {
		t.Fatalf("recordedServer received no requests")
	}
	return values[name][len(values[name])-1]
}

func newClient(t *testing.T, baseURL string) *anthropic.Client {
	t.Helper()
	client, err := anthropic.New(anthropic.Config{
		APIKey:  "test-key",
		BaseURL: baseURL,
		Model:   "claude-sonnet-4-5",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return client
}

func TestNewRejectsMissingRequiredConfiguration(t *testing.T) {
	t.Parallel()

	if _, err := anthropic.New(anthropic.Config{Model: "claude-sonnet-4-5"}); err == nil {
		t.Fatal("New() error = nil, want missing API key rejection")
	}
	if _, err := anthropic.New(anthropic.Config{APIKey: "k"}); err == nil {
		t.Fatal("New() error = nil, want missing model rejection")
	}
	if _, err := anthropic.New(anthropic.Config{APIKey: "k", Model: "claude-sonnet-4-5", MaxTokens: -1}); err == nil {
		t.Fatal("New() error = nil, want negative max tokens rejection")
	}
}

func TestGenerateTranslatesRequestAndResponse(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{
		"content": [
			{"type": "text", "text": "Let me roll."},
			{"type": "tool_use", "id": "toolu-1", "name": "roll", "input": {"n": 2}},
			{"type": "tool_use", "id": "toolu-2", "name": "roll", "input": {}}
		],
		"usage": {"input_tokens": 12, "output_tokens": 3}
	}`)
	defer recorder.server.Close()

	client := newClient(t, recorder.server.URL)
	_, err := client.Generate(context.Background(), model.Request{
		Messages: []model.Message{
			{Role: model.RoleSystem, Content: "Be concise."},
			{Role: model.RoleUser, Content: "Roll a die."},
			{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
				{ID: "toolu-1", Name: "roll", Args: json.RawMessage(`{"n":1}`)},
				{ID: "toolu-2", Name: "roll", Args: json.RawMessage(`{}`)},
			}},
			{Role: model.RoleTool, ToolCallID: "toolu-1", ToolName: "roll", Content: "rolled 1"},
			{Role: model.RoleTool, ToolCallID: "toolu-2", ToolName: "roll", Content: "rolled 1"},
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

	if got := recorder.lastHeader(t, "x-api-key"); got != "test-key" {
		t.Fatalf("x-api-key = %q, want test-key", got)
	}
	if got := recorder.lastHeader(t, "anthropic-version"); got != "2023-06-01" {
		t.Fatalf("anthropic-version = %q, want 2023-06-01", got)
	}

	var sent struct {
		Model     string `json:"model"`
		MaxTokens int    `json:"max_tokens"`
		System    string `json:"system"`
		Messages  []struct {
			Role    string `json:"role"`
			Content []struct {
				Type      string          `json:"type"`
				Text      string          `json:"text,omitempty"`
				ID        string          `json:"id,omitempty"`
				Name      string          `json:"name,omitempty"`
				Input     json.RawMessage `json:"input,omitempty"`
				ToolUseID string          `json:"tool_use_id,omitempty"`
				Content   string          `json:"content,omitempty"`
			} `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Name        string          `json:"name"`
			InputSchema json.RawMessage `json:"input_schema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(recorder.lastBody(t)), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}
	if sent.Model != "claude-sonnet-4-5" {
		t.Fatalf("wire model = %q", sent.Model)
	}
	if sent.MaxTokens != anthropic.DefaultMaxTokens {
		t.Fatalf("wire max_tokens = %d, want the default %d", sent.MaxTokens, anthropic.DefaultMaxTokens)
	}
	if sent.System != "Be concise." {
		t.Fatalf("wire system = %q, want the instructions as top-level system guidance", sent.System)
	}
	if len(sent.Tools) != 1 || sent.Tools[0].Name != "roll" ||
		string(sent.Tools[0].InputSchema) != `{"type":"object","properties":{"n":{"type":"integer"}}}` {
		t.Fatalf("wire tools = %#v", sent.Tools)
	}

	// System guidance leaves the turn list; the two tool results and the
	// following user prompt merge into one user turn; the assistant's
	// calls become tool_use blocks.
	if len(sent.Messages) != 3 {
		t.Fatalf("wire turns = %d, want 3 (user, assistant, user): %#v", len(sent.Messages), sent.Messages)
	}
	if sent.Messages[0].Role != "user" || len(sent.Messages[0].Content) != 1 ||
		sent.Messages[0].Content[0].Type != "text" || sent.Messages[0].Content[0].Text != "Roll a die." {
		t.Fatalf("first turn = %#v", sent.Messages[0])
	}
	second := sent.Messages[1]
	if second.Role != "assistant" || len(second.Content) != 2 ||
		second.Content[0].Type != "tool_use" || second.Content[0].ID != "toolu-1" ||
		string(second.Content[0].Input) != `{"n":1}` ||
		second.Content[1].Type != "tool_use" || second.Content[1].ID != "toolu-2" ||
		string(second.Content[1].Input) != "{}" {
		t.Fatalf("assistant turn = %#v", second)
	}
	third := sent.Messages[2]
	if third.Role != "user" || len(third.Content) != 3 ||
		third.Content[0].Type != "tool_result" || third.Content[0].ToolUseID != "toolu-1" || third.Content[0].Content != "rolled 1" ||
		third.Content[1].Type != "tool_result" || third.Content[1].ToolUseID != "toolu-2" ||
		third.Content[2].Type != "text" || third.Content[2].Text != "Now roll two." {
		t.Fatalf("merged user turn = %#v", third)
	}
}

func TestGenerateNormalizesResponse(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{
		"content": [
			{"type": "text", "text": "Rolled"},
			{"type": "text", "text": "a 2."},
			{"type": "thinking", "thinking": "internal reasoning"}
		],
		"usage": {"input_tokens": 12, "output_tokens": 3}
	}`)
	defer recorder.server.Close()

	response, err := newClient(t, recorder.server.URL).Generate(context.Background(), model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "roll"}},
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if response.Message.Role != model.RoleAssistant {
		t.Fatalf("role = %q, want assistant", response.Message.Role)
	}
	if response.Message.Content != "Rolled\na 2." {
		t.Fatalf("content = %q, want text blocks joined on newlines", response.Message.Content)
	}
	if response.Usage != (model.Usage{InputTokens: 12, OutputTokens: 3}) {
		t.Fatalf("usage = %#v", response.Usage)
	}
}

func TestGenerateClassifiesProviderErrors(t *testing.T) {
	t.Parallel()

	t.Run("retryable on 529", func(t *testing.T) {
		t.Parallel()
		recorder := newRecordedServer(529, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)
		defer recorder.server.Close()

		_, err := newClient(t, recorder.server.URL).Generate(context.Background(), model.Request{
			Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
		})
		var apiError *anthropic.APIError
		if !errors.As(err, &apiError) {
			t.Fatalf("Generate() error = %v, want APIError", err)
		}
		if apiError.Code != "overloaded_error" || apiError.Message != "Overloaded" {
			t.Fatalf("APIError = {%q %q}", apiError.Code, apiError.Message)
		}
		if !model.IsRetryable(err) {
			t.Fatal("model.IsRetryable(err) = false, want true for 5xx")
		}
	})

	t.Run("not retryable on 400", func(t *testing.T) {
		t.Parallel()
		recorder := newRecordedServer(400, `{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens is required"}}`)
		defer recorder.server.Close()

		_, err := newClient(t, recorder.server.URL).Generate(context.Background(), model.Request{
			Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
		})
		var apiError *anthropic.APIError
		if !errors.As(err, &apiError) {
			t.Fatalf("Generate() error = %v, want APIError", err)
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
	recorder := newRecordedServer(http.StatusOK, `{"content":[{"type":"text","text":"late"}]}`)
	defer recorder.server.Close()

	_, err := newClient(t, recorder.server.URL).Generate(ctx, model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Generate() error = %v, want context.Canceled", err)
	}
	var transportError *anthropic.TransportError
	if !errors.As(err, &transportError) {
		t.Fatalf("Generate() error = %v, want TransportError", err)
	}
	if model.IsRetryable(err) {
		t.Fatal("model.IsRetryable(err) = true, want false for cancellation")
	}
}

func TestGenerateMapsOutputSchemaToOutputConfig(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{
		"content": [{"type": "text", "text": "{\"city\":\"Lagos\"}"}],
		"usage": {"input_tokens": 12, "output_tokens": 3}
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
		OutputConfig *struct {
			Format struct {
				Type   string          `json:"type"`
				Schema json.RawMessage `json:"schema"`
			} `json:"format"`
		} `json:"output_config"`
	}
	if err := json.Unmarshal([]byte(recorder.lastBody(t)), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}
	if sent.OutputConfig == nil || sent.OutputConfig.Format.Type != "json_schema" {
		t.Fatalf("output_config = %#v, want json_schema", sent.OutputConfig)
	}
	if string(sent.OutputConfig.Format.Schema) != string(schema) {
		t.Fatalf("output_config.format.schema = %s, want the request schema", sent.OutputConfig.Format.Schema)
	}

	// Without a schema the field stays off the wire entirely.
	if _, err := client.Generate(context.Background(), model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	}); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	sent.OutputConfig = nil
	if err := json.Unmarshal([]byte(recorder.lastBody(t)), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}
	if sent.OutputConfig != nil {
		t.Fatalf("output_config = %#v, want nil without an output schema", sent.OutputConfig)
	}
}

func TestGenerateMapsPromptPartsToImageBlocks(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{
		"content": [{"type": "text", "text": "a red square"}],
		"usage": {"input_tokens": 10, "output_tokens": 4}
	}`)
	defer recorder.server.Close()
	client := newClient(t, recorder.server.URL)

	if _, err := client.Generate(context.Background(), model.Request{Messages: []model.Message{
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
			Role    string           `json:"role"`
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(recorder.lastBody(t)), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}

	wantBlocks := []map[string]any{
		{"type": "text", "text": "what is this"},
		{"type": "image", "source": map[string]any{"type": "url", "url": "https://example.com/a.png"}},
		{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": "AQI="}},
	}
	if len(sent.Messages) != 1 || sent.Messages[0].Role != "user" {
		t.Fatalf("wire messages = %#v", sent.Messages)
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

func TestConfiguredSamplingControlsOnWire(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{}`)
	temperature, topP := 0.4, 0.9
	client, err := anthropic.New(anthropic.Config{
		APIKey:      "test-key",
		BaseURL:     recorder.server.URL,
		Model:       "claude-sonnet-4-5",
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

	var sent map[string]any
	if err := json.Unmarshal([]byte(recorder.lastBody(t)), &sent); err != nil {
		t.Fatalf("decode sent body: %v", err)
	}
	if sent["temperature"] != 0.4 || sent["top_p"] != 0.9 {
		t.Fatalf("sampling controls = %v", sent)
	}

	// A default config keeps the controls off the wire; max_tokens keeps
	// its required default.
	defaultClient := newClient(t, recorder.server.URL)
	_, _ = defaultClient.Generate(context.Background(), model.Request{Messages: []model.Message{
		{Role: model.RoleUser, Content: "hi"},
	}})
	var plain map[string]any
	if err := json.Unmarshal([]byte(recorder.lastBody(t)), &plain); err != nil {
		t.Fatalf("decode sent body: %v", err)
	}
	for _, key := range []string{"temperature", "top_p"} {
		if _, present := plain[key]; present {
			t.Fatalf("unset %q must stay off the wire, got %v", key, plain[key])
		}
	}
	if plain["max_tokens"] != float64(anthropic.DefaultMaxTokens) {
		t.Fatalf("max_tokens = %v, want the required default", plain["max_tokens"])
	}
}

func TestNewRejectsInvalidSamplingControls(t *testing.T) {
	t.Parallel()

	invalid := func(v float64) *float64 { return &v }
	for name, cfg := range map[string]anthropic.Config{
		"negative temperature": {APIKey: "k", Model: "m", Temperature: invalid(-0.1)},
		"hot temperature":      {APIKey: "k", Model: "m", Temperature: invalid(1.1)},
		"top P above one":      {APIKey: "k", Model: "m", TopP: invalid(1.1)},
	} {
		if _, err := anthropic.New(cfg); err == nil {
			t.Fatalf("%s: New() = nil error, want rejection", name)
		}
	}
}

func TestThinkingConfigMapsToWire(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		thinking *anthropic.ThinkingConfig
		effort   string
		want     string // the thinking JSON the wire must carry
		wantEff  string
	}{
		{
			name:     "adaptive",
			thinking: &anthropic.ThinkingConfig{Adaptive: true},
			want:     `{"type":"adaptive"}`,
		},
		{
			name:     "budget",
			thinking: &anthropic.ThinkingConfig{BudgetTokens: 4096},
			want:     `{"type":"enabled","budget_tokens":4096}`,
		},
		{
			name:     "disabled",
			thinking: &anthropic.ThinkingConfig{Disabled: true},
			want:     `{"type":"disabled"}`,
		},
		{
			name:     "adaptive with effort",
			thinking: &anthropic.ThinkingConfig{Adaptive: true},
			effort:   "high",
			want:     `{"type":"adaptive"}`,
			wantEff:  "high",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			recorder := newRecordedServer(http.StatusOK, `{}`)
			client, err := anthropic.New(anthropic.Config{
				APIKey:   "test-key",
				BaseURL:  recorder.server.URL,
				Model:    "claude-sonnet-4-5",
				Thinking: tc.thinking,
				Effort:   tc.effort,
			})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			_, _ = client.Generate(context.Background(), model.Request{Messages: []model.Message{
				{Role: model.RoleUser, Content: "hi"},
			}})

			var sent struct {
				Thinking *json.RawMessage `json:"thinking"`
				Effort   string           `json:"effort"`
			}
			if err := json.Unmarshal([]byte(recorder.lastBody(t)), &sent); err != nil {
				t.Fatalf("decode sent body: %v", err)
			}
			if tc.want == "" {
				if sent.Thinking != nil {
					t.Fatalf("thinking = %s, want absent", *sent.Thinking)
				}
			} else {
				if sent.Thinking == nil || string(*sent.Thinking) != tc.want {
					t.Fatalf("thinking = %v, want %s", sent.Thinking, tc.want)
				}
			}
			if sent.Effort != tc.wantEff {
				t.Fatalf("effort = %q, want %q", sent.Effort, tc.wantEff)
			}
		})
	}

	// A default config keeps both off the wire.
	recorder := newRecordedServer(http.StatusOK, `{}`)
	defaultClient := newClient(t, recorder.server.URL)
	_, _ = defaultClient.Generate(context.Background(), model.Request{Messages: []model.Message{
		{Role: model.RoleUser, Content: "hi"},
	}})
	if strings.Contains(recorder.lastBody(t), `"thinking"`) || strings.Contains(recorder.lastBody(t), `"effort"`) {
		t.Fatalf("default config put thinking controls on the wire: %s", recorder.lastBody(t))
	}
}

func TestNewRejectsInvalidThinkingConfig(t *testing.T) {
	t.Parallel()

	base := anthropic.Config{APIKey: "k", Model: "claude-sonnet-4-5"}

	// Exactly one field must be set.
	for name, thinking := range map[string]*anthropic.ThinkingConfig{
		"empty":     {},
		"conflict":  {Adaptive: true, Disabled: true},
		"negative":  {BudgetTokens: -1},
		"two modes": {Adaptive: true, BudgetTokens: 100},
	} {
		if _, err := anthropic.New(anthropic.Config{APIKey: "k", Model: "m", Thinking: thinking}); err == nil {
			t.Fatalf("%s config: New() error = nil, want rejection", name)
		}
	}
	_ = base

	if _, err := anthropic.New(anthropic.Config{APIKey: "k", Model: "m", Thinking: &anthropic.ThinkingConfig{BudgetTokens: 0}}); err == nil {
		t.Fatal("zero-budget config: New() error = nil, want rejection")
	}
}

func TestGenerateNormalizesThinkingBlocks(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{"content":[
		{"type":"thinking","thinking":"2+2 is 4","signature":"sig-1"},
		{"type":"redacted_thinking","data":"enc"},
		{"type":"text","text":"The answer is 4."},
		{"type":"tool_use","id":"toolu_1","name":"calc","input":{"op":"add"}}
	],"usage":{"input_tokens":10,"output_tokens":5}}`)
	client := newClient(t, recorder.server.URL)

	response, err := client.Generate(context.Background(), model.Request{Messages: []model.Message{
		{Role: model.RoleUser, Content: "hi"},
	}})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	thinking := response.Message.Thinking
	if len(thinking) != 2 {
		t.Fatalf("thinking blocks = %d, want 2", len(thinking))
	}
	if thinking[0].Text != "2+2 is 4" || thinking[0].Signature != "sig-1" {
		t.Fatalf("signed block = %#v, want text with signature", thinking[0])
	}
	if thinking[1].Redacted != "enc" || thinking[1].Text != "" {
		t.Fatalf("redacted block = %#v", thinking[1])
	}
	if response.Message.Content != "The answer is 4." || len(response.Message.ToolCalls) != 1 {
		t.Fatalf("message = %#v", response.Message)
	}

	// History carrying thinking replays the blocks before text and calls.
	history := model.Request{Messages: []model.Message{
		{Role: model.RoleUser, Content: "hi"},
		{Role: model.RoleAssistant, Content: "4", Thinking: thinking,
			ToolCalls: []model.ToolCall{{ID: "toolu_1", Name: "calc", Args: json.RawMessage(`{}`)}}},
	}}
	_, _ = client.Generate(context.Background(), history)
	var sent struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type      string `json:"type"`
				Thinking  string `json:"thinking"`
				Signature string `json:"signature"`
				Data      string `json:"data"`
				Text      string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(recorder.lastBody(t)), &sent); err != nil {
		t.Fatalf("decode sent body: %v", err)
	}
	blocks := sent.Messages[1].Content
	if len(blocks) != 4 || blocks[0].Type != "thinking" || blocks[1].Type != "redacted_thinking" ||
		blocks[2].Type != "text" || blocks[3].Type != "tool_use" {
		t.Fatalf("assistant blocks = %#v, want thinking, redacted, text, tool_use", blocks)
	}
	if blocks[0].Thinking != "2+2 is 4" || blocks[0].Signature != "sig-1" || blocks[1].Data != "enc" {
		t.Fatalf("replayed reasoning = %#v, want signatures preserved", blocks[:2])
	}
}
