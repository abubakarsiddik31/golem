package gemini_test

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
	"github.com/abubakarsiddik31/golem/providers/gemini"
)

// recordedServer replies with a scripted status and body and records the
// body and auth header of every request it receives.
type recordedServer struct {
	server *httptest.Server

	mu      sync.Mutex
	bodies  []string
	apiKeys []string
	paths   []string
}

func newRecordedServer(status int, body string) *recordedServer {
	recorder := &recordedServer{}
	recorder.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)
		recorder.mu.Lock()
		recorder.bodies = append(recorder.bodies, string(payload))
		recorder.apiKeys = append(recorder.apiKeys, r.Header.Get("x-goog-api-key"))
		recorder.paths = append(recorder.paths, r.URL.Path)
		recorder.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	return recorder
}

func (r *recordedServer) last(t *testing.T) bodyAndHeaders {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.bodies) == 0 {
		t.Fatal("recordedServer received no requests")
	}
	return bodyAndHeaders{
		body:   r.bodies[len(r.bodies)-1],
		apiKey: r.apiKeys[len(r.apiKeys)-1],
		path:   r.paths[len(r.paths)-1],
	}
}

type bodyAndHeaders struct {
	body   string
	apiKey string
	path   string
}

func (r *recordedServer) requestCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.bodies)
}

func newClient(t *testing.T, baseURL string) *gemini.Client {
	t.Helper()
	client, err := gemini.New(gemini.Config{
		APIKey:  "test-key",
		BaseURL: baseURL,
		Model:   "gemini-2.5-flash",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return client
}

func TestNewRejectsMissingRequiredConfiguration(t *testing.T) {
	t.Parallel()

	if _, err := gemini.New(gemini.Config{Model: "gemini-2.5-flash"}); err == nil {
		t.Fatal("New() error = nil, want missing API key rejection")
	}
	if _, err := gemini.New(gemini.Config{APIKey: "k"}); err == nil {
		t.Fatal("New() error = nil, want missing model rejection")
	}
}

func TestGenerateTranslatesRequestAndResponse(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{
		"candidates": [{
			"content": {"role": "model", "parts": [
				{"text": "Rolling."},
				{"functionCall": {"name": "roll", "args": {"n":2}}},
				{"functionCall": {"name": "roll", "args": {}}}
			]},
			"finishReason": "STOP"
		}],
		"usageMetadata": {"promptTokenCount": 12, "candidatesTokenCount": 3}
	}`)
	defer recorder.server.Close()

	client := newClient(t, recorder.server.URL)
	_, err := client.Generate(context.Background(), model.Request{
		Messages: []model.Message{
			{Role: model.RoleSystem, Content: "Be concise."},
			{Role: model.RoleUser, Content: "Roll a die."},
			{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
				{ID: "call-1", Name: "roll", Args: json.RawMessage(`{"n":1}`)},
			}},
			{Role: model.RoleTool, ToolCallID: "call-1", ToolName: "roll", Content: "rolled 1"},
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

	sent := recorder.last(t)
	if sent.apiKey != "test-key" {
		t.Fatalf("x-goog-api-key = %q, want test-key", sent.apiKey)
	}
	if !strings.HasSuffix(sent.path, "/v1beta/models/gemini-2.5-flash:generateContent") {
		t.Fatalf("path = %q, want the generateContent endpoint", sent.path)
	}

	var request struct {
		Contents []struct {
			Role  string `json:"role"`
			Parts []struct {
				Text         string `json:"text,omitempty"`
				FunctionCall *struct {
					Name string          `json:"name"`
					Args json.RawMessage `json:"args"`
				} `json:"functionCall,omitempty"`
				FunctionResponse *struct {
					Name     string          `json:"name"`
					Response json.RawMessage `json:"response"`
				} `json:"functionResponse,omitempty"`
			} `json:"parts"`
		} `json:"contents"`
		SystemInstruction *struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"systemInstruction"`
		Tools []struct {
			FunctionDeclarations []struct {
				Name       string          `json:"name"`
				Parameters json.RawMessage `json:"parameters"`
			} `json:"functionDeclarations"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(sent.body), &request); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}
	if request.SystemInstruction == nil || len(request.SystemInstruction.Parts) != 1 ||
		request.SystemInstruction.Parts[0].Text != "Be concise." {
		t.Fatalf("systemInstruction = %#v, want the instructions", request.SystemInstruction)
	}
	if len(request.Tools) != 1 || len(request.Tools[0].FunctionDeclarations) != 1 ||
		request.Tools[0].FunctionDeclarations[0].Name != "roll" {
		t.Fatalf("tools = %#v", request.Tools)
	}
	// Function responses and the following user prompt merge into one
	// user turn; the assistant's call becomes a functionCall part.
	if len(request.Contents) != 3 {
		t.Fatalf("contents = %#v, want 3 turns", request.Contents)
	}
	if request.Contents[1].Role != "model" || len(request.Contents[1].Parts) != 1 ||
		request.Contents[1].Parts[0].FunctionCall == nil ||
		string(request.Contents[1].Parts[0].FunctionCall.Args) != `{"n":1}` {
		t.Fatalf("model turn = %#v", request.Contents[1])
	}
	third := request.Contents[2]
	if third.Role != "user" || len(third.Parts) != 2 ||
		third.Parts[0].FunctionResponse == nil || third.Parts[0].FunctionResponse.Name != "roll" ||
		string(third.Parts[0].FunctionResponse.Response) != `{"result":"rolled 1"}` ||
		third.Parts[1].Text != "Now roll two." {
		t.Fatalf("merged user turn = %#v", third)
	}

	// The scripted response normalizes: text joins, calls get stable
	// generated IDs, empty args become {}.
	response, err := client.Generate(context.Background(), model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "again"}},
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if response.Message.Content != "Rolling." {
		t.Fatalf("content = %q", response.Message.Content)
	}
	if len(response.Message.ToolCalls) != 2 {
		t.Fatalf("tool calls = %#v, want 2 with generated IDs", response.Message.ToolCalls)
	}
	first, second := response.Message.ToolCalls[0], response.Message.ToolCalls[1]
	if first.ID != "call-1" || first.Name != "roll" || string(first.Args) != `{"n":2}` {
		t.Fatalf("first tool call = %#v", first)
	}
	if second.ID != "call-2" || second.Name != "roll" || string(second.Args) != `{}` {
		t.Fatalf("second tool call = %#v", second)
	}
	if response.Usage != (model.Usage{InputTokens: 12, OutputTokens: 3}) {
		t.Fatalf("usage = %#v", response.Usage)
	}
}

func TestGenerateNormalizesResponse(t *testing.T) {
	t.Parallel()

	t.Run("joins text parts", func(t *testing.T) {
		t.Parallel()
		recorder := newRecordedServer(http.StatusOK, `{
			"candidates": [{"content": {"role": "model", "parts": [
				{"text": "Rolled"},
				{"text": "a 2."}
			]}}],
			"usageMetadata": {"promptTokenCount": 5, "candidatesTokenCount": 2}
		}`)
		defer recorder.server.Close()

		response, err := newClient(t, recorder.server.URL).Generate(context.Background(), model.Request{
			Messages: []model.Message{{Role: model.RoleUser, Content: "roll"}},
		})
		if err != nil {
			t.Fatalf("Generate() error = %v", err)
		}
		if response.Message.Content != "Rolled\na 2." {
			t.Fatalf("content = %q, want text parts joined on newlines", response.Message.Content)
		}
	})

	t.Run("rejects empty candidates", func(t *testing.T) {
		t.Parallel()
		recorder := newRecordedServer(http.StatusOK, `{"candidates": []}`)
		defer recorder.server.Close()

		_, err := newClient(t, recorder.server.URL).Generate(context.Background(), model.Request{
			Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
		})
		var decodeError *gemini.DecodeError
		if !errors.As(err, &decodeError) {
			t.Fatalf("Generate() error = %v, want DecodeError", err)
		}
	})
}

func TestGenerateMapsOutputSchemaToGenerationConfig(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{
		"candidates": [{"content": {"role": "model", "parts": [{"text": "{\"city\":\"Lagos\"}"}]}}]
	}`)
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
		GenerationConfig *struct {
			ResponseMimeType string          `json:"responseMimeType"`
			ResponseSchema   json.RawMessage `json:"responseSchema"`
		} `json:"generationConfig"`
	}
	if err := json.Unmarshal([]byte(recorder.last(t).body), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}
	if sent.GenerationConfig == nil ||
		sent.GenerationConfig.ResponseMimeType != "application/json" ||
		string(sent.GenerationConfig.ResponseSchema) != string(schema) {
		t.Fatalf("generationConfig = %#v, want JSON responses shaped by the schema", sent.GenerationConfig)
	}

	// Without a schema the field stays off the wire entirely.
	if _, err := client.Generate(context.Background(), model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	}); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	sent.GenerationConfig = nil
	if err := json.Unmarshal([]byte(recorder.last(t).body), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}
	if sent.GenerationConfig != nil {
		t.Fatalf("generationConfig = %#v, want nil without an output schema", sent.GenerationConfig)
	}
}

// TestGenerateRejectsOutputSchemaCombinedWithTools is a regression test:
// the GenerateContent API rejects JSON response mode combined with
// function calling, so the adapter must fail at request encoding — before
// any network call — with guidance toward tool-mode output.
func TestGenerateRejectsOutputSchemaCombinedWithTools(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{}`)
	defer recorder.server.Close()
	client := newClient(t, recorder.server.URL)

	request := model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "report"}},
		ToolSpecs: []model.ToolSpec{{
			Name:        "lookup",
			Description: "Look something up.",
			Schema:      json.RawMessage(`{"type":"object"}`),
		}},
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
	}

	call := func(name string, run func() error) {
		t.Run(name, func(t *testing.T) {
			err := run()
			var decodeErr *gemini.DecodeError
			if !errors.As(err, &decodeErr) {
				t.Fatalf("error = %v, want *gemini.DecodeError", err)
			}
			if decodeErr.Stage != "encode request" {
				t.Fatalf("DecodeError stage = %q, want %q", decodeErr.Stage, "encode request")
			}
			for _, want := range []string{"output schema", "tool", "WithOutputTool"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q does not mention %q", err, want)
				}
			}
			if got := recorder.requestCount(); got != 0 {
				t.Fatalf("adapter sent %d HTTP request(s); the rejection must happen before any network call", got)
			}
		})
	}
	call("Generate", func() error {
		_, err := client.Generate(context.Background(), request)
		return err
	})
	call("GenerateStream", func() error {
		_, err := client.GenerateStream(context.Background(), request, func(model.Delta) error { return nil })
		return err
	})
}

func TestGenerateClassifiesProviderErrors(t *testing.T) {
	t.Parallel()

	t.Run("retryable on 503", func(t *testing.T) {
		t.Parallel()
		recorder := newRecordedServer(503, `{"error": {"code": 503, "message": "Model overloaded", "status": "UNAVAILABLE"}}`)
		defer recorder.server.Close()

		_, err := newClient(t, recorder.server.URL).Generate(context.Background(), model.Request{
			Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
		})
		var apiError *gemini.APIError
		if !errors.As(err, &apiError) {
			t.Fatalf("Generate() error = %v, want APIError", err)
		}
		if apiError.Code != "UNAVAILABLE" || apiError.Message != "Model overloaded" {
			t.Fatalf("APIError = {%q %q}", apiError.Code, apiError.Message)
		}
		if !model.IsRetryable(err) {
			t.Fatal("model.IsRetryable(err) = false, want true for 5xx")
		}
	})

	t.Run("not retryable on 400", func(t *testing.T) {
		t.Parallel()
		recorder := newRecordedServer(400, `{"error": {"code": 400, "message": "Invalid", "status": "INVALID_ARGUMENT"}}`)
		defer recorder.server.Close()

		_, err := newClient(t, recorder.server.URL).Generate(context.Background(), model.Request{
			Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
		})
		if model.IsRetryable(err) {
			t.Fatal("model.IsRetryable(err) = true, want false for 400")
		}
	})
}

func TestGeneratePropagatesCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	recorder := newRecordedServer(http.StatusOK, `{"candidates": [{"content": {"parts": [{"text": "late"}]}}]}`)
	defer recorder.server.Close()

	_, err := newClient(t, recorder.server.URL).Generate(ctx, model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Generate() error = %v, want context.Canceled", err)
	}
	var transportError *gemini.TransportError
	if !errors.As(err, &transportError) {
		t.Fatalf("Generate() error = %v, want TransportError", err)
	}
	if model.IsRetryable(err) {
		t.Fatal("model.IsRetryable(err) = true, want false for cancellation")
	}
}

func TestGenerateMapsPromptPartsToInlineDataAndFileData(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{
		"candidates": [{"content": {"parts": [{"text": "a red square"}], "role": "model"}, "finishReason": "STOP"}],
		"usageMetadata": {"promptTokenCount": 10, "candidatesTokenCount": 4}
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
		Contents []struct {
			Role  string           `json:"role"`
			Parts []map[string]any `json:"parts"`
		} `json:"contents"`
	}
	if err := json.Unmarshal([]byte(recorder.last(t).body), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}

	wantParts := []map[string]any{
		{"text": "what is this"},
		{"fileData": map[string]any{"fileUri": "https://example.com/a.png"}},
		{"inlineData": map[string]any{"mimeType": "image/png", "data": "AQI="}},
	}
	if len(sent.Contents) != 1 || sent.Contents[0].Role != "user" {
		t.Fatalf("wire contents = %#v", sent.Contents)
	}
	got := sent.Contents[0].Parts
	if len(got) != len(wantParts) {
		t.Fatalf("user turn parts = %#v, want %d parts", got, len(wantParts))
	}
	for i, want := range wantParts {
		if !reflect.DeepEqual(got[i], want) {
			t.Fatalf("part[%d] = %#v, want %#v", i, got[i], want)
		}
	}
}
