package groq

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/openai"
)

// TestPresetFillsBaseURLAndRequiresKey verifies the preset rules on
// the exact config New will build.
func TestPresetFillsBaseURLAndRequiresKey(t *testing.T) {
	t.Parallel()

	if _, err := Preset(openai.Config{Model: "llama-3.3-70b-versatile"}); err == nil {
		t.Fatal("Preset accepted a missing API key")
	}
	preset, err := Preset(openai.Config{APIKey: "gsk_test"})
	if err != nil {
		t.Fatalf("Preset error = %v", err)
	}
	if preset.BaseURL != DefaultBaseURL {
		t.Errorf("BaseURL = %q, want the Groq default", preset.BaseURL)
	}
	// An explicit base URL — a proxy, a test server — is kept.
	preset, err = Preset(openai.Config{APIKey: "gsk_test", BaseURL: "http://127.0.0.1:1/v1"})
	if err != nil {
		t.Fatalf("Preset error = %v", err)
	}
	if preset.BaseURL != "http://127.0.0.1:1/v1" {
		t.Errorf("BaseURL = %q, want the caller's", preset.BaseURL)
	}
}

// TestNewSpeaksTheOpenAIWire verifies the preset client against a
// local server standing in for Groq: same wire protocol, same path.
func TestNewSpeaksTheOpenAIWire(t *testing.T) {
	t.Parallel()

	var path, auth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)
		_ = payload
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"fast"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(server.Close)

	client, err := New(openai.Config{APIKey: "gsk_test", BaseURL: server.URL, Model: ModelLlama3370B})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	response, err := client.Generate(t.Context(), openaiRequest())
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if response.Message.Content != "fast" {
		t.Errorf("content = %q", response.Message.Content)
	}
	if path != "/chat/completions" {
		t.Errorf("path = %q", path)
	}
	if auth != "Bearer gsk_test" {
		t.Errorf("auth = %q", auth)
	}
}

// TestNewRejectsMissingKey keeps the preset honest at construction.
func TestNewRejectsMissingKey(t *testing.T) {
	t.Parallel()

	_, err := New(openai.Config{Model: ModelDeepSeekR1DistillLlama})
	if err == nil || !strings.Contains(err.Error(), "API key is required") {
		t.Fatalf("New error = %v, want a missing-key failure", err)
	}
}

func openaiRequest() model.Request {
	return model.Request{Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}}
}
