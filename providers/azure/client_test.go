package azure_test

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
	"github.com/abubakarsiddik31/golem/providers/azure"
)

// recordedServer replies with a scripted status and body and records the
// path, query, body, and auth header of every request it receives.
type recordedServer struct {
	server *httptest.Server

	mu      sync.Mutex
	bodies  []string
	apiKeys []string
	paths   []string
	queries []string
}

func newRecordedServer(status int, body string) *recordedServer {
	recorder := &recordedServer{}
	recorder.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)
		recorder.mu.Lock()
		recorder.bodies = append(recorder.bodies, string(payload))
		recorder.apiKeys = append(recorder.apiKeys, r.Header.Get("api-key"))
		recorder.paths = append(recorder.paths, r.URL.Path)
		recorder.queries = append(recorder.queries, r.URL.RawQuery)
		recorder.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	return recorder
}

func (r *recordedServer) last(t *testing.T) recordedRequest {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.bodies) == 0 {
		t.Fatal("recordedServer received no requests")
	}
	return recordedRequest{
		body:   r.bodies[len(r.bodies)-1],
		apiKey: r.apiKeys[len(r.apiKeys)-1],
		path:   r.paths[len(r.paths)-1],
		query:  r.queries[len(r.queries)-1],
	}
}

type recordedRequest struct {
	body   string
	apiKey string
	path   string
	query  string
}

func newClient(t *testing.T, endpoint string) *azure.Client {
	t.Helper()
	client, err := azure.New(azure.Config{
		APIKey:     "test-key",
		Endpoint:   endpoint,
		Deployment: "gpt-4o",
		APIVersion: "2024-10-21",
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

	full := azure.Config{APIKey: "k", Endpoint: "https://r.openai.azure.com", Deployment: "gpt-4o", APIVersion: "2024-10-21"}
	tests := []struct {
		name string
		cfg  azure.Config
	}{
		{"missing API key", azure.Config{Endpoint: full.Endpoint, Deployment: full.Deployment, APIVersion: full.APIVersion}},
		{"missing endpoint", azure.Config{APIKey: full.APIKey, Deployment: full.Deployment, APIVersion: full.APIVersion}},
		{"missing deployment", azure.Config{APIKey: full.APIKey, Endpoint: full.Endpoint, APIVersion: full.APIVersion}},
		{"missing API version", azure.Config{APIKey: full.APIKey, Endpoint: full.Endpoint, Deployment: full.Deployment}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := azure.New(test.cfg); err == nil {
				t.Fatal("New() error = nil, want rejection")
			}
		})
	}
}

func TestGenerateTargetsDeploymentEndpoint(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{
		"choices": [{"message": {"role": "assistant", "content": "42"}}],
		"usage": {"prompt_tokens": 12, "completion_tokens": 1}
	}`)
	defer recorder.server.Close()

	client := newClient(t, recorder.server.URL+"/")
	response, err := client.Generate(context.Background(), model.Request{
		Messages: []model.Message{
			{Role: model.RoleSystem, Content: "Be concise."},
			{Role: model.RoleUser, Content: "Answer?"},
		},
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if response.Message.Content != "42" || response.Usage != (model.Usage{InputTokens: 12, OutputTokens: 1}) {
		t.Fatalf("response = %#v", response)
	}

	sent := recorder.last(t)
	if !strings.HasSuffix(sent.path, "/openai/deployments/gpt-4o/chat/completions") {
		t.Fatalf("path = %q, want the deployment chat endpoint", sent.path)
	}
	if sent.query != "api-version=2024-10-21" {
		t.Fatalf("query = %q, want the API version", sent.query)
	}
	if sent.apiKey != "test-key" {
		t.Fatalf("api-key header = %q, want test-key", sent.apiKey)
	}
	// The model is addressed by deployment, not on the wire.
	if strings.Contains(sent.body, `"model"`) {
		t.Fatalf("wire body = %s, want no model field", sent.body)
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
			Type string `json:"type"`
		} `json:"response_format"`
	}
	if err := json.Unmarshal([]byte(recorder.last(t).body), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}
	if sent.ResponseFormat == nil || sent.ResponseFormat.Type != "json_schema" {
		t.Fatalf("response_format = %#v, want json_schema", sent.ResponseFormat)
	}
}

func TestGenerateClassifiesProviderErrors(t *testing.T) {
	t.Parallel()

	t.Run("retryable on 429", func(t *testing.T) {
		t.Parallel()
		recorder := newRecordedServer(429, `{"error": {"code": "429", "message": "Rate limit reached"}}`)
		defer recorder.server.Close()

		_, err := newClient(t, recorder.server.URL).Generate(context.Background(), userPrompt())
		var apiError *azure.APIError
		if !errors.As(err, &apiError) {
			t.Fatalf("Generate() error = %v, want APIError", err)
		}
		if apiError.Message != "Rate limit reached" {
			t.Fatalf("APIError message = %q", apiError.Message)
		}
		if !model.IsRetryable(err) {
			t.Fatal("model.IsRetryable(err) = false, want true for 429")
		}
	})

	t.Run("not retryable on 400", func(t *testing.T) {
		t.Parallel()
		recorder := newRecordedServer(400, `{"error": {"code": "BadRequest", "message": "Invalid request"}}`)
		defer recorder.server.Close()

		_, err := newClient(t, recorder.server.URL).Generate(context.Background(), userPrompt())
		if model.IsRetryable(err) {
			t.Fatal("model.IsRetryable(err) = true, want false for 400")
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
	var transportError *azure.TransportError
	if !errors.As(err, &transportError) {
		t.Fatalf("Generate() error = %v, want TransportError", err)
	}
	if model.IsRetryable(err) {
		t.Fatal("model.IsRetryable(err) = true, want false for cancellation")
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
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(recorder.last(t).body), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}

	var content []map[string]any
	if err := json.Unmarshal(sent.Messages[0].Content, &content); err != nil {
		t.Fatalf("user content is not an array: %s", sent.Messages[0].Content)
	}
	wantParts := []map[string]any{
		{"type": "text", "text": "what is this"},
		{"type": "image_url", "image_url": map[string]any{"url": "https://example.com/a.png"}},
		{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,AQI="}},
	}
	if len(content) != len(wantParts) {
		t.Fatalf("content array length = %d, want %d: %s", len(content), len(wantParts), sent.Messages[0].Content)
	}
	for i, want := range wantParts {
		if !reflect.DeepEqual(content[i], want) {
			t.Fatalf("content[%d] = %#v, want %#v", i, content[i], want)
		}
	}
}
