package anthropic_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/anthropic"
	"github.com/abubakarsiddik31/golem/tokens"
)

func TestCounterPricesMessagesSystemAndTools(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{"input_tokens": 42}`)
	counter, err := anthropic.NewCounter(anthropic.Config{
		APIKey: "test-key", BaseURL: recorder.server.URL, Model: "claude-sonnet-4-5",
	})
	if err != nil {
		t.Fatalf("NewCounter() error = %v", err)
	}

	count, err := counter.CountTokens(context.Background(), tokens.CountInput{
		Messages: []model.Message{
			{Role: model.RoleSystem, Content: "be brief"},
			{Role: model.RoleUser, Content: "hi"},
		},
		Tools: []model.ToolSpec{{Name: "t", Description: "d", Schema: []byte(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatalf("CountTokens() error = %v", err)
	}
	if count != 42 {
		t.Fatalf("CountTokens() = %d, want 42", count)
	}

	var sent struct {
		Model    string `json:"model"`
		System   string `json:"system"`
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
		Tools []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal([]byte(recorder.lastBody(t)), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}
	if sent.Model != "claude-sonnet-4-5" || sent.System != "be brief" || len(sent.Messages) != 1 || len(sent.Tools) != 1 {
		t.Fatalf("count request = %+v, want model, system, one message, and one tool", sent)
	}
}

func TestCounterRejectsMissingConfigBeforeNetwork(t *testing.T) {
	t.Parallel()

	if _, err := anthropic.NewCounter(anthropic.Config{Model: "m"}); err == nil {
		t.Fatal("NewCounter() without API key error = nil, want failure")
	}
	if _, err := anthropic.NewCounter(anthropic.Config{APIKey: "k"}); err == nil {
		t.Fatal("NewCounter() without model error = nil, want failure")
	}
}

func TestCounterClassifiesProviderErrors(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusTooManyRequests, `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`)
	counter, err := anthropic.NewCounter(anthropic.Config{
		APIKey: "test-key", BaseURL: recorder.server.URL, Model: "claude-sonnet-4-5",
	})
	if err != nil {
		t.Fatalf("NewCounter() error = %v", err)
	}

	_, err = counter.CountTokens(context.Background(), tokens.CountInput{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	})
	var apiErr *anthropic.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *anthropic.APIError", err)
	}
	if apiErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("StatusCode = %d, want 429", apiErr.StatusCode)
	}
}
