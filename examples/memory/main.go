// Command memory gives an agent a persistent notebook: it writes the
// main MEMORY.md and a focused note, then searches them. It runs offline
// against a scripted model with the in-process store.
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/memory"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
)

type user struct{ ID string }

func main() {
	tools, err := memory.Tools(memory.Config[user]{
		Store:     memory.NewMemory(),
		Namespace: func(u user) string { return u.ID },
	})
	if err != nil {
		fmt.Println("memory.Tools:", err)
		return
	}

	call := func(id, name, args string) model.Response {
		return model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{
			ID:   id,
			Name: name,
			Args: json.RawMessage(args),
		}}}}
	}
	client := testmodel.New().Respond(
		call("call-1", "write_memory", `{"path":"MEMORY.md","content":"The user prefers concise answers."}`),
		call("call-2", "write_memory", `{"path":"notes/project.md","content":"The project uses Go 1.26."}`),
		call("call-3", "search_memory", `{"query":"Go 1.26"}`),
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "Remembered the preference and the project note."}},
	)
	agent, err := golem.New[user, string](client,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithInstructions[user, string](memory.Instructions),
		golem.WithTools[user, string](tools...),
	)
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}

	result, err := agent.Run(context.Background(), golem.RunContext[user]{Deps: user{ID: "ada"}}, "Remember what matters.")
	if err != nil {
		fmt.Println("Run:", err)
		return
	}

	fmt.Println("answer:", result.Output)
	for _, message := range result.Messages {
		if message.Role == model.RoleTool {
			fmt.Printf("%s:\n%s\n", message.ToolName, message.Content)
		}
	}
}
