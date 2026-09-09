package bedrock_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/bedrock"
	"github.com/abubakarsiddik31/golem/tokens"
)

func TestCounterPricesMessagesAndSystemOnTheCountTokensPath(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusOK, `{"inputTokens": 27}`, nil)
	counter, err := bedrock.NewCounter(bedrock.Config{
		Credentials: bedrock.Credentials{AccessKeyID: "ak", SecretAccessKey: "sk"},
		Region:      "us-east-1", BaseURL: recorder.server.URL, Model: "anthropic.claude-3-haiku-20240307-v1:0",
	})
	if err != nil {
		t.Fatalf("NewCounter() error = %v", err)
	}

	count, err := counter.CountTokens(context.Background(), tokens.CountInput{
		Messages: []model.Message{
			{Role: model.RoleSystem, Content: "be brief"},
			{Role: model.RoleUser, Content: "hi"},
		},
		// Tools are priced by other providers but the Bedrock count
		// request has no field for them; sending one must not leak onto
		// the wire.
		Tools: []model.ToolSpec{{Name: "t", Description: "d", Schema: []byte(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatalf("CountTokens() error = %v", err)
	}
	if count != 27 {
		t.Fatalf("CountTokens() = %d, want 27", count)
	}

	last := recorder.last(t)
	if last.path != "/model/anthropic.claude-3-haiku-20240307-v1:0/count-tokens" {
		t.Fatalf("request path = %q, want the count-tokens endpoint", last.path)
	}
	var sent struct {
		Input struct {
			Converse struct {
				Messages []struct {
					Role string `json:"role"`
				} `json:"messages"`
				System []map[string]any `json:"system"`
			} `json:"converse"`
		} `json:"input"`
	}
	if err := json.Unmarshal([]byte(last.body), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}
	if len(sent.Input.Converse.Messages) != 1 || len(sent.Input.Converse.System) != 1 {
		t.Fatalf("count request = %+v, want one message and system guidance under input.converse", sent)
	}
	if last.authorizer == "" {
		t.Fatal("count request is unsigned; want SigV4")
	}
}

func TestCounterRejectsMissingConfigBeforeNetwork(t *testing.T) {
	t.Parallel()

	if _, err := bedrock.NewCounter(bedrock.Config{Region: "us-east-1", Model: "m"}); err == nil {
		t.Fatal("NewCounter() without credentials error = nil, want failure")
	}
	if _, err := bedrock.NewCounter(bedrock.Config{
		Credentials: bedrock.Credentials{AccessKeyID: "ak", SecretAccessKey: "sk"}, Model: "m",
	}); err == nil {
		t.Fatal("NewCounter() without region error = nil, want failure")
	}
}

func TestCounterClassifiesProviderErrors(t *testing.T) {
	t.Parallel()

	recorder := newRecordedServer(http.StatusBadRequest,
		`{"__type":"ValidationException","message":"Access Denied. Please ensure your model ID is a base foundation model."}`, nil)
	counter, err := bedrock.NewCounter(bedrock.Config{
		Credentials: bedrock.Credentials{AccessKeyID: "ak", SecretAccessKey: "sk"},
		Region:      "us-east-1", BaseURL: recorder.server.URL, Model: "us.anthropic.claude-3-haiku-20240307-v1:0",
	})
	if err != nil {
		t.Fatalf("NewCounter() error = %v", err)
	}

	_, err = counter.CountTokens(context.Background(), tokens.CountInput{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	})
	var apiErr *bedrock.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *bedrock.APIError", err)
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("StatusCode = %d, want 400", apiErr.StatusCode)
	}
}
