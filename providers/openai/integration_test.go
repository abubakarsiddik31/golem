package openai_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/openai"
	"github.com/abubakarsiddik31/golem/tool"
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

// TestLiveGenerateStream streams one real generation and checks that the
// fragments reassemble into the returned response. Opt-in like
// TestLiveAgentRoundTrip; the same env vars configure it.
func TestLiveGenerateStream(t *testing.T) {
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
	if response.Usage.InputTokens == 0 && response.Usage.OutputTokens == 0 {
		t.Log("warning: provider reported zero streamed usage")
	}
	t.Logf("live fragments: %d, content: %q, usage: %+v", len(fragments), joined, response.Usage)
}

// TestLiveAgentToolRoundTrip runs one real tool exchange: the model must
// request the declared tool with arguments, Golem executes it with the
// typed dependency value, and the model answers from the result. Opt-in
// like TestLiveAgentRoundTrip; the same env vars configure it.
func TestLiveAgentToolRoundTrip(t *testing.T) {
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

	// The tool answer is unknowable without execution: it depends on the
	// dependency value and on the arguments the model supplies.
	getPlayerName := tool.MustNew(tool.Tool[string]{
		Name:        "get_player_name",
		Description: "Get the display name of a player by numeric id.",
		Schema: json.RawMessage(`{
			"type": "object",
			"properties": {"player_id": {"type": "integer", "description": "The player's numeric id."}},
			"required": ["player_id"]
		}`),
		Exec: func(ctx context.Context, playerName string, args json.RawMessage) (string, error) {
			var input struct {
				PlayerID int `json:"player_id"`
			}
			if err := json.Unmarshal(args, &input); err != nil {
				return "", fmt.Errorf("decode get_player_name args: %w", err)
			}
			return fmt.Sprintf("%s (id %d)", playerName, input.PlayerID), nil
		},
	})

	agent, err := golem.New[string, string](client,
		golem.DecodeFunc[string](func(ctx context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithInstructions[string, string]("Always use the get_player_name tool; never guess names."),
		golem.WithTools[string, string](getPlayerName),
		golem.WithMaxAttempts[string, string](3),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[string]{Deps: "Anne"},
		"Look up the player with id 7 and tell me their display name.")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// The tool must have actually executed with the typed dependency and
	// the model-supplied argument.
	toolResult := ""
	for _, message := range result.Messages {
		if message.Role == model.RoleTool && message.ToolName == "get_player_name" {
			toolResult = message.Content
		}
	}
	if toolResult != "Anne (id 7)" {
		t.Fatalf("tool result = %q, want %q; messages = %#v", toolResult, "Anne (id 7)", result.Messages)
	}
	if !strings.Contains(result.Output, "Anne") {
		t.Fatalf("output = %q, want it derived from the tool result", result.Output)
	}
	t.Logf("live output: %q, tool result: %q, usage: %+v", result.Output, toolResult, result.Usage)
}
