package bedrock_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/bedrock"
)

// TestLiveAgentRoundTrip runs one real generation through golem.Agent. It
// is opt-in: without GOLEM_BEDROCK_ACCESS_KEY_ID it skips, keeping the
// default test run network-free per the contract-test rules.
// GOLEM_BEDROCK_SECRET_ACCESS_KEY, GOLEM_BEDROCK_SESSION_TOKEN,
// GOLEM_BEDROCK_REGION, and GOLEM_BEDROCK_MODEL configure the target.
func TestLiveAgentRoundTrip(t *testing.T) {
	accessKeyID := os.Getenv("GOLEM_BEDROCK_ACCESS_KEY_ID")
	secretAccessKey := os.Getenv("GOLEM_BEDROCK_SECRET_ACCESS_KEY")
	region := os.Getenv("GOLEM_BEDROCK_REGION")
	modelID := os.Getenv("GOLEM_BEDROCK_MODEL")
	if accessKeyID == "" || secretAccessKey == "" || region == "" || modelID == "" {
		t.Skip("GOLEM_BEDROCK_ACCESS_KEY_ID, GOLEM_BEDROCK_SECRET_ACCESS_KEY, GOLEM_BEDROCK_REGION, or GOLEM_BEDROCK_MODEL not set; skipping live integration test")
	}

	client, err := bedrock.New(bedrock.Config{
		Credentials: bedrock.Credentials{
			AccessKeyID:     accessKeyID,
			SecretAccessKey: secretAccessKey,
			SessionToken:    os.Getenv("GOLEM_BEDROCK_SESSION_TOKEN"),
		},
		Region: region,
		Model:  modelID,
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
