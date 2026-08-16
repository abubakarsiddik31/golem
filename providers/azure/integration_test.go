package azure_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/azure"
)

// TestLiveAgentRoundTrip runs one real generation through golem.Agent. It
// is opt-in: without GOLEM_AZURE_OPENAI_API_KEY it skips, keeping the
// default test run network-free per the contract-test rules. The
// GOLEM_AZURE_OPENAI_ENDPOINT, GOLEM_AZURE_OPENAI_DEPLOYMENT, and
// GOLEM_AZURE_OPENAI_API_VERSION variables configure the target.
func TestLiveAgentRoundTrip(t *testing.T) {
	apiKey := os.Getenv("GOLEM_AZURE_OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("GOLEM_AZURE_OPENAI_API_KEY not set; skipping live integration test")
	}
	endpoint := os.Getenv("GOLEM_AZURE_OPENAI_ENDPOINT")
	deployment := os.Getenv("GOLEM_AZURE_OPENAI_DEPLOYMENT")
	apiVersion := os.Getenv("GOLEM_AZURE_OPENAI_API_VERSION")
	if endpoint == "" || deployment == "" || apiVersion == "" {
		t.Skip("GOLEM_AZURE_OPENAI_ENDPOINT, GOLEM_AZURE_OPENAI_DEPLOYMENT, or GOLEM_AZURE_OPENAI_API_VERSION not set; skipping live integration test")
	}

	client, err := azure.New(azure.Config{
		APIKey:     apiKey,
		Endpoint:   endpoint,
		Deployment: deployment,
		APIVersion: apiVersion,
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
	t.Logf("live output: %q, usage: %+v", result.Output, result.Usage)
}
