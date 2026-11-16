// Command compaction keeps a long conversation bounded by summarizing
// its older turns with golem.SummarizeHistory, while the newest messages
// stay verbatim. It runs offline against a scripted model and a
// deterministic summarizer.
package main

import (
	"context"
	"fmt"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
)

func main() {
	// A real summarizer calls a cheaper model; this one is deterministic
	// so the example stays offline.
	summarize := func(_ context.Context, messages []model.Message) (string, error) {
		return fmt.Sprintf("%d earlier messages covered the inn, the map, and the route north.", len(messages)), nil
	}

	client := testmodel.New().Respond(model.Response{
		Message: model.Message{Role: model.RoleAssistant, Content: "Head north at dawn."},
	})
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		// Keep the newest four messages verbatim and summarize the rest.
		golem.WithHistoryProcessor[struct{}, string](golem.SummarizeHistory(summarize, 4)),
	)
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}

	history := []model.Message{
		{Role: model.RoleUser, Content: "We reached the inn."},
		{Role: model.RoleAssistant, Content: "The innkeeper mentioned a map."},
		{Role: model.RoleUser, Content: "Buy the map."},
		{Role: model.RoleAssistant, Content: "Bought it; the route north is marked."},
		{Role: model.RoleUser, Content: "Stock up on supplies."},
		{Role: model.RoleAssistant, Content: "Supplies are packed."},
		{Role: model.RoleUser, Content: "Check the weather."},
		{Role: model.RoleAssistant, Content: "Clear skies tomorrow."},
	}

	result, err := agent.RunWithHistory(context.Background(), golem.RunContext[struct{}]{}, history, "What next?")
	if err != nil {
		fmt.Println("RunWithHistory:", err)
		return
	}

	fmt.Println("answer:", result.Output)
	fmt.Println("request sent to the model:")
	for i, message := range client.Requests()[0].Messages {
		fmt.Printf("  %d %-9s %.70q\n", i, message.Role, message.Content)
	}
}
