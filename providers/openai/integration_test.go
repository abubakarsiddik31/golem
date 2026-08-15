package openai_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/openai"
)

// TestLiveAgentRoundTrip runs one real generation through golem.Agent. It
// is opt-in: without GOLEM_OPENAI_API_KEY it skips, keeping the default
// test run network-free per the contract-test rules. GOLEM_OPENAI_BASE_URL
// and GOLEM_OPENAI_MODEL override the endpoint and model.
func TestLiveAgentRoundTrip(t *testing.T) {
	apiKey := os.Getenv("GOLEM_OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("GOLEM_OPENAI_API_KEY not set; skipping live integration test")
	}

	baseURL := os.Getenv("GOLEM_OPENAI_BASE_URL")
	modelName := os.Getenv("GOLEM_OPENAI_MODEL")
	if modelName == "" {
		modelName = "gpt-4o-mini"
	}

	client, err := openai.New(openai.Config{
		APIKey:  apiKey,
		BaseURL: baseURL,
		Model:   modelName,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{},
		"Reply with exactly the word: pong")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if strings.TrimSpace(result.Output) == "" {
		t.Fatalf("live run returned empty output; messages = %#v", result.Messages)
	}
	if result.Usage.InputTokens == 0 && result.Usage.OutputTokens == 0 {
		t.Log("warning: provider reported zero usage")
	}
	t.Logf("live output: %q, usage: %+v", result.Output, result.Usage)
}
