package gemini_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"testing"

	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/gemini"
	"github.com/abubakarsiddik31/golem/tokens"
)

func TestCounterPricesContentsSystemAndTools(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{"totalTokens": 33}`)
	counter, err := gemini.NewCounter(gemini.Config{
		APIKey: "test-key", BaseURL: recorder.server.URL, Model: "gemini-2.5-flash",
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
	if count != 33 {
		t.Fatalf("CountTokens() = %d, want 33", count)
	}

	last := recorder.last(t)
	if last.path != "/v1beta/models/gemini-2.5-flash:countTokens" {
		t.Fatalf("request path = %q, want the countTokens endpoint", last.path)
	}
	var sent struct {
		Contents []struct {
			Role string `json:"role"`
		} `json:"contents"`
		SystemInstruction *map[string]any  `json:"systemInstruction"`
		Tools             []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal([]byte(last.body), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}
	if len(sent.Contents) != 1 || sent.SystemInstruction == nil || len(sent.Tools) != 1 {
		t.Fatalf("count request = %+v, want one content, a system instruction, and one tool", sent)
	}
}

func TestCounterRejectsMissingConfigBeforeNetwork(t *testing.T) {
	t.Parallel()

	if _, err := gemini.NewCounter(gemini.Config{Model: "m"}); err == nil {
		t.Fatal("NewCounter() without API key error = nil, want failure")
	}
	if _, err := gemini.NewCounter(gemini.Config{APIKey: "k"}); err == nil {
		t.Fatal("NewCounter() without model error = nil, want failure")
	}
}

func TestCounterClassifiesProviderErrors(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusBadRequest, `{"error":{"code":400,"message":"bad request","status":"INVALID_ARGUMENT"}}`)
	counter, err := gemini.NewCounter(gemini.Config{
		APIKey: "test-key", BaseURL: recorder.server.URL, Model: "gemini-2.5-flash",
	})
	if err != nil {
		t.Fatalf("NewCounter() error = %v", err)
	}

	_, err = counter.CountTokens(context.Background(), tokens.CountInput{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	})
	var apiErr *gemini.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *gemini.APIError", err)
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("StatusCode = %d, want 400", apiErr.StatusCode)
	}
}
