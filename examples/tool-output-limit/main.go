// Command tool-output-limit bounds a large tool result before it enters
// history: the model sees the head and tail plus a marker, not the whole
// payload on every later request. It runs offline against a scripted
// model.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
	"github.com/abubakarsiddik31/golem/tool"
)

func main() {
	readLog := tool.MustNew(tool.Tool[struct{}]{
		Name:        "read_log",
		Description: "Read the build log.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(context.Context, struct{}, json.RawMessage) (tool.Result, error) {
			return tool.Text(strings.Repeat("build output line\n", 400) + "BUILD FAILED: test timeout\n"), nil
		},
	})

	client := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{
			ID:   "call-1",
			Name: "read_log",
			Args: json.RawMessage(`{}`),
		}}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "The build failed on a test timeout."}},
	)
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithTools[struct{}, string](readLog),
		golem.WithToolOutputLimit[struct{}, string](400),
	)
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "Why did the build fail?")
	if err != nil {
		fmt.Println("Run:", err)
		return
	}
	fmt.Println("answer:", result.Output)
	for _, message := range result.Messages {
		if message.Role == model.RoleTool {
			fmt.Printf("tool result (%d bytes):\n%s\n", len(message.Content), message.Content)
		}
	}
}
