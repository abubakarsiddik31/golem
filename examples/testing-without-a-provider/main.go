// Command testing-without-a-provider runs an agent against a scripted
// fake model: no network, no credentials, fully deterministic. This is
// the pattern Golem's own tests use, and the pattern applications should
// use to test agent behavior.
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

// scriptedModel requests the tool once, then answers from the tool result.
type scriptedModel struct{ calls int }

func (m *scriptedModel) Generate(ctx context.Context, request model.Request) (model.Response, error) {
	for _, message := range request.Messages {
		if message.Role == model.RoleTool {
			return model.Response{
				Message: model.Message{Role: model.RoleAssistant,
					Content: "the answer came from the tool: " + message.Content},
			}, nil
		}
	}
	m.calls++
	return model.Response{
		Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "get_player_name", Args: json.RawMessage(`{}`)},
		}},
	}, nil
}

func main() {
	getPlayerName := tool.MustNew(tool.Tool[string]{
		Name:        "get_player_name",
		Description: "Get the player's name.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, playerName string, args json.RawMessage) (string, error) {
			return playerName, nil
		},
	})

	agent, err := golem.New[string, string](&scriptedModel{},
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithTools[string, string](getPlayerName),
	)
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}

	result, err := agent.Run(context.Background(), golem.RunContext[string]{Deps: "Anne"}, "who wins?")
	if err != nil {
		fmt.Println("Run:", err)
		return
	}

	fmt.Println(result.Output)
	for i, message := range result.Messages {
		fmt.Printf("message %d: role=%s content=%q\n", i, message.Role, message.Content)
	}
}
