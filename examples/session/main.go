// Command session persists a conversation through the session port:
// WithSession loads the stored history before each run and saves the
// conversation after it, so chained runs pass no history by hand. The
// store here is the offline in-memory implementation; a file or
// database backend implements the same two-method interface.
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/session"
	"github.com/abubakarsiddik31/golem/testmodel"
	"github.com/abubakarsiddik31/golem/tool"
)

func main() {
	getPlayerName := tool.MustNew(tool.Tool[string]{
		Name:        "get_player_name",
		Description: "Get the player's name.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, playerName string, args json.RawMessage) (tool.Result, error) {
			return tool.Text(playerName), nil
		},
	})

	client := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "Anne plays white"},
			Usage: model.Usage{InputTokens: 120, OutputTokens: 8}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "Anne opened with the Queen's Gambit"},
			Usage: model.Usage{InputTokens: 260, OutputTokens: 14}},
	)
	agent, err := golem.New[string, string](client,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithTools[string, string](getPlayerName),
	)
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}

	store := session.NewMemory()

	// The first run starts the conversation and saves it.
	first, err := agent.Run(context.Background(), golem.RunContext[string]{Deps: "Anne"},
		"who plays white?", golem.WithSession(store), golem.WithConversationID("game-1"))
	if err != nil {
		fmt.Println("Run:", err)
		return
	}
	fmt.Println("turn 1:", first.Output)

	// The second run passes no history: the store supplies it, and the
	// model sees turn 1 before the new prompt.
	second, err := agent.Run(context.Background(), golem.RunContext[string]{Deps: "Anne"},
		"what was the opening move?", golem.WithSession(store), golem.WithConversationID("game-1"))
	if err != nil {
		fmt.Println("Run:", err)
		return
	}
	fmt.Println("turn 2:", second.Output)
	fmt.Printf("stored %d messages under %s\n", len(second.Messages), second.ConversationID)

	// A failure is not the end of a conversation: partial evidence is
	// saved too, so the run can resume from the store. This one fails
	// at the decode stage and still leaves a resumable transcript.
	broken, err := golem.New[struct{}, int](testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "forty two"}},
	), golem.DecodeFunc[int](func(_ context.Context, r model.Response) (int, error) {
		return 0, &model.ModelRetry{Err: fmt.Errorf("%q is not a number", r.Message.Content)}
	}))
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}
	if _, err := broken.Run(context.Background(), golem.RunContext[struct{}]{},
		"how many squares?", golem.WithSession(store), golem.WithConversationID("game-1")); err != nil {
		fmt.Println("turn 3 failed:", err)
	}
	saved, _ := store.Load(context.Background(), "game-1")
	fmt.Printf("the store still holds %d messages, failure included\n", len(saved))
}
