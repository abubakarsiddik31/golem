package golem_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

// diceModel scripts the tool exchange: it requests the player-name tool
// once, then produces a final answer. Real applications implement
// model.Model with a provider adapter.
type diceModel struct{ requests []model.Request }

func (m *diceModel) Generate(ctx context.Context, request model.Request) (model.Response, error) {
	m.requests = append(m.requests, request)
	for _, message := range request.Messages {
		if message.Role == model.RoleTool {
			return model.Response{
				Message: model.Message{Role: model.RoleAssistant, Content: fmt.Sprintf("winner: %s", message.Content)},
				Usage:   model.Usage{InputTokens: 54, OutputTokens: 2},
			}, nil
		}
	}
	return model.Response{
		Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "get_player_name", Args: json.RawMessage(`{}`)},
		}},
		Usage: model.Usage{InputTokens: 54, OutputTokens: 2},
	}, nil
}

// ExampleAgent demonstrates an agent that executes a typed tool with an
// explicit dependency value and returns the full run evidence.
func ExampleAgent() {
	getPlayerName := tool.MustNew(tool.Tool[string]{
		Name:        "get_player_name",
		Description: "Get the player's name.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, playerName string, args json.RawMessage) (string, error) {
			return playerName, nil
		},
	})

	agent, err := golem.New[string, string](
		&diceModel{},
		golem.DecodeFunc[string](func(ctx context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithTools[string, string](getPlayerName),
	)
	if err != nil {
		log.Fatal(err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[string]{Deps: "Anne"}, "My guess is 4")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(result.Output)
	fmt.Println(len(result.Messages), "messages,", result.Usage.OutputTokens, "output tokens")
	// Output:
	// winner: Anne
	// 4 messages, 4 output tokens
}
