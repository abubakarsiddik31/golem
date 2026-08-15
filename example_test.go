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

// conversationModel answers with the last user prompt it has seen.
type conversationModel struct{}

func (m *conversationModel) Generate(ctx context.Context, request model.Request) (model.Response, error) {
	last := ""
	for _, message := range request.Messages {
		if message.Role == model.RoleUser {
			last = message.Content
		}
	}
	return model.Response{
		Message: model.Message{Role: model.RoleAssistant, Content: fmt.Sprintf("heard: %s", last)},
	}, nil
}

// ExampleAgent_RunWithHistory continues a conversation across two runs:
// the first result's messages become the second run's history, and the
// second result carries the full chained conversation.
func ExampleAgent_RunWithHistory() {
	agent, err := golem.New[struct{}, string](&conversationModel{},
		golem.DecodeFunc[string](func(ctx context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}))
	if err != nil {
		log.Fatal(err)
	}
	runCtx := golem.RunContext[struct{}]{}

	first, err := agent.Run(context.Background(), runCtx, "hello")
	if err != nil {
		log.Fatal(err)
	}
	second, err := agent.RunWithHistory(context.Background(), runCtx, first.Messages, "goodbye")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(first.Output)
	fmt.Println(second.Output)
	fmt.Println(len(second.Messages), "messages in the chained conversation")
	// Output:
	// heard: hello
	// heard: goodbye
	// 4 messages in the chained conversation
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
