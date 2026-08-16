// Command tools runs an agent whose tool receives a typed dependency
// value: the model requests the lookup, Golem executes it with the run's
// Deps, and the model answers from the result.
//
// Set OPENAI_API_KEY (and optionally OPENAI_MODEL) to run it.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/openai"
	"github.com/abubakarsiddik31/golem/tool"
)

// roster is the dependency value every tool in the run receives.
type roster struct {
	PlayerName string
}

func main() {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		fmt.Println("Set OPENAI_API_KEY (and optionally OPENAI_MODEL) to run this example.")
		return
	}
	modelName := os.Getenv("OPENAI_MODEL")
	if modelName == "" {
		modelName = "gpt-4o-mini"
	}

	client, err := openai.New(openai.Config{APIKey: apiKey, Model: modelName})
	if err != nil {
		fmt.Println("openai.New:", err)
		return
	}

	getPlayerName := tool.MustNew(tool.Tool[roster]{
		Name:        "get_player_name",
		Description: "Get the display name of a player by numeric id.",
		Schema: json.RawMessage(`{
			"type": "object",
			"properties": {"player_id": {"type": "integer", "description": "The player's numeric id."}},
			"required": ["player_id"]
		}`),
		Exec: func(ctx context.Context, deps roster, args json.RawMessage) (string, error) {
			var input struct {
				PlayerID int `json:"player_id"`
			}
			if err := json.Unmarshal(args, &input); err != nil {
				return "", fmt.Errorf("decode get_player_name args: %w", err)
			}
			return fmt.Sprintf("%s (id %d)", deps.PlayerName, input.PlayerID), nil
		},
	})

	agent, err := golem.New[roster, string](client,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithInstructions[roster, string]("Always use the get_player_name tool; never guess names."),
		golem.WithTools[roster, string](getPlayerName),
	)
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}

	result, err := agent.Run(context.Background(), golem.RunContext[roster]{Deps: roster{PlayerName: "Anne"}},
		"Look up the player with id 7 and tell me their display name.")
	if err != nil {
		fmt.Println("Run:", err)
		return
	}

	fmt.Println(result.Output)
	for _, message := range result.Messages {
		if message.Role == model.RoleTool {
			fmt.Printf("tool %s returned %q\n", message.ToolName, message.Content)
		}
	}
}
