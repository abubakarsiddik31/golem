// Command memory gives an agent a persistent notebook: it writes the
// main MEMORY.md and a focused note, then a second run recalls the
// notebook through memory.Snapshot and golem.WithPromptContext. It runs
// offline against scripted models with the in-process store.
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
	cfg := memory.Config[user]{
		Store:     memory.NewMemory(),
		Namespace: func(u user) string { return u.ID },
	}
	tools, err := memory.Tools(cfg)
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
	writer := testmodel.New().Respond(
		call("call-1", "write_memory", `{"path":"MEMORY.md","content":"The user prefers concise answers."}`),
		call("call-2", "write_memory", `{"path":"notes/project.md","content":"The project uses Go 1.26."}`),
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "Remembered the preference and the project note."}},
	)
	writeAgent, err := golem.New[user, string](writer,
		decoder(),
		golem.WithInstructions[user, string](memory.Instructions),
		golem.WithTools[user, string](tools...),
	)
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}
	if _, err := writeAgent.Run(context.Background(), golem.RunContext[user]{Deps: user{ID: "ada"}}, "Remember what matters."); err != nil {
		fmt.Println("write run:", err)
		return
	}

	reader := testmodel.New().Respond(model.Response{
		Message: model.Message{Role: model.RoleAssistant, Content: "You prefer concise answers, and the project uses Go 1.26."},
	})
	readAgent, err := golem.New[user, string](reader,
		decoder(),
		golem.WithPromptContext[user, string](func(ctx context.Context, runCtx golem.RunContext[user]) ([]model.Message, error) {
			message, ok, err := memory.Snapshot(ctx, cfg, runCtx.Deps)
			if err != nil || !ok {
				return nil, err
			}
			return []model.Message{message}, nil
		}),
	)
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}
	result, err := readAgent.Run(context.Background(), golem.RunContext[user]{Deps: user{ID: "ada"}}, "What do you remember?")
	if err != nil {
		fmt.Println("read run:", err)
		return
	}

	fmt.Println("answer:", result.Output)
	fmt.Println("recalled context:")
	fmt.Println(reader.Requests()[0].Messages[0].Content)
}

func decoder() golem.DecodeFunc[string] {
	return golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
		return response.Message.Content, nil
	})
}
