package gemini_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/gemini"
)

// TestLiveAgentRoundTrip runs one real generation through golem.Agent. It
// is opt-in: without GOLEM_GEMINI_API_KEY it skips, keeping the default
// test run network-free per the contract-test rules.
// GOLEM_GEMINI_BASE_URL and GOLEM_GEMINI_MODEL override the endpoint and
// model.
func TestLiveAgentRoundTrip(t *testing.T) {
	apiKey := os.Getenv("GOLEM_GEMINI_API_KEY")
	if apiKey == "" {
		t.Skip("GOLEM_GEMINI_API_KEY not set; skipping live integration test")
	}

	baseURL := os.Getenv("GOLEM_GEMINI_BASE_URL")
	modelName := os.Getenv("GOLEM_GEMINI_MODEL")
	if modelName == "" {
		modelName = "gemini-2.5-flash"
	}

	client, err := gemini.New(gemini.Config{
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

// TestLiveGenerateStream streams one real generation and checks that the
// fragments reassemble into the returned response. Opt-in like
// TestLiveAgentRoundTrip; the same env vars configure it.
func TestLiveGenerateStream(t *testing.T) {
	apiKey := os.Getenv("GOLEM_GEMINI_API_KEY")
	if apiKey == "" {
		t.Skip("GOLEM_GEMINI_API_KEY not set; skipping live integration test")
	}
	baseURL := os.Getenv("GOLEM_GEMINI_BASE_URL")
	modelName := os.Getenv("GOLEM_GEMINI_MODEL")
	if modelName == "" {
		modelName = "gemini-2.5-flash"
	}

	client, err := gemini.New(gemini.Config{
		APIKey:  apiKey,
		BaseURL: baseURL,
		Model:   modelName,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	var fragments []string
	response, err := client.GenerateStream(context.Background(), model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "Count from one to five as digits, like 12345."}},
	}, func(d model.Delta) error {
		fragments = append(fragments, d.Content)
		return nil
	})
	if err != nil {
		t.Fatalf("GenerateStream() error = %v", err)
	}

	joined := strings.Join(fragments, "")
	if joined != response.Message.Content {
		t.Fatalf("joined fragments %q != assembled content %q", joined, response.Message.Content)
	}
	if strings.TrimSpace(joined) == "" {
		t.Fatalf("streamed content is empty; fragments = %#v", fragments)
	}
	t.Logf("live fragments: %d, content: %q, usage: %+v", len(fragments), joined, response.Usage)
}
