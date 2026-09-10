// Command run-ids shows a conversation's identity: every run mints a
// fresh run ID, but runs chained through RunWithHistory share one
// conversation ID — inherited from the history's most recent identified
// message, so the association survives a storage round-trip with no
// session object. A fork continues the same history under a new
// identity, and every event repeats the pair, so a shared agent's
// interleaved event streams stay attributable.
//
// The run is fully scripted — no network, no credentials.
package main

import (
	"context"
	"fmt"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
)

func main() {
	client := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "hello"}}).Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "hello again"}}).Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "continuing"}}).Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "forked"}})
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) {
			return r.Message.Content, nil
		}),
		golem.WithRunEvents[struct{}, string](func(event golem.RunEvent) {
			fmt.Printf("  event %-12s run=%s conversation=%s\n",
				event.Kind, event.RunID, event.ConversationID)
		}))
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}

	// Two plain runs start two conversations.
	first, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "hi")
	if err != nil {
		fmt.Println("Run:", err)
		return
	}
	second, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "hi again")
	if err != nil {
		fmt.Println("Run:", err)
		return
	}
	fmt.Printf("plain runs:     run %s conv %s | run %s conv %s (fresh each)\n",
		first.RunID, first.ConversationID, second.RunID, second.ConversationID)

	// Chaining through history continues the first conversation — a new
	// run ID, the same conversation ID.
	continued, err := agent.RunWithHistory(context.Background(), golem.RunContext[struct{}]{},
		first.Messages, "and then?")
	if err != nil {
		fmt.Println("RunWithHistory:", err)
		return
	}
	fmt.Printf("continued run:  run %s conv %s (same conversation)\n",
		continued.RunID, continued.ConversationID)

	// Forking: the same history under a fresh identity. The history
	// cannot override an explicit conversation ID.
	fork, err := agent.RunWithHistory(context.Background(), golem.RunContext[struct{}]{},
		first.Messages, "fork this", golem.WithConversationID(golem.NewID()))
	if err != nil {
		fmt.Println("RunWithHistory:", err)
		return
	}
	fmt.Printf("forked run:     run %s conv %s (new conversation, same history)\n",
		fork.RunID, fork.ConversationID)

	// The conversation ID rides the message JSON, so storage round-trips
	// preserve it: these prints read the durable contract back.
	fmt.Printf("stored message identity: %s / %s\n",
		continued.Messages[0].RunID, continued.Messages[0].ConversationID)
}
