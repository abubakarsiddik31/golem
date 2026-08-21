// Command run-events observes an executing run through WithRunEvents:
// every provider call attempt and tool execution reported as it happens.
// It runs against a scripted fake model — no network, no credentials,
// fully deterministic.
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
	"github.com/abubakarsiddik31/golem/tool"
)

func main() {
	getPlayerName := tool.MustNew(tool.Tool[string]{
		Name:        "get_player_name",
		Description: "Get the player's name.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, playerName string, args json.RawMessage) (string, error) {
			return playerName, nil
		},
	})

	client := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "get_player_name", Args: json.RawMessage(`{}`)},
		}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "Anne wins"}},
	)
	agent, err := golem.New[string, string](client,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithTools[string, string](getPlayerName),
		golem.WithRunEvents[string, string](report),
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
	fmt.Println("output:", result.Output)
}

// report prints one line per event. Which fields carry meaning depends
// on the kind; the rest are zero.
func report(event golem.RunEvent) {
	switch event.Kind {
	case golem.EventModelStart:
		fmt.Printf("%-16s turn=%d attempt=%d\n", event.Kind, event.Turn, event.Attempt)
	case golem.EventModelEnd:
		fmt.Printf("%-16s turn=%d attempt=%d tokens(in=%d out=%d)\n",
			event.Kind, event.Turn, event.Attempt,
			event.Usage.InputTokens, event.Usage.OutputTokens)
	case golem.EventToolStart:
		fmt.Printf("%-16s %s args=%s\n", event.Kind, event.ToolName, event.Args)
	case golem.EventToolEnd:
		fmt.Printf("%-16s %s result=%q\n", event.Kind, event.ToolName, event.Result)
	case golem.EventOutputRejected:
		fmt.Printf("%-16s correction round=%d: %v\n", event.Kind, event.Attempt, event.Err)
	}
}
