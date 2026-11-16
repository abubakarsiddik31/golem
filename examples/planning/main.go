// Command planning gives an agent a task list it maintains through the
// planning toolset: write the plan, move a step in progress, then mark
// it completed. It runs offline against a scripted model.
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/planning"
	"github.com/abubakarsiddik31/golem/testmodel"
)

func main() {
	plan, err := planning.Tools(planning.Config[struct{}]{Store: planning.NewMemory()})
	if err != nil {
		fmt.Println("planning.Tools:", err)
		return
	}

	client := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{
			ID:   "call-1",
			Name: "write_plan",
			Args: json.RawMessage(`{"items":[
				{"content":"Read the issue","activeForm":"Reading the issue"},
				{"content":"Write the tests"}
			]}`),
		}}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{
			ID:   "call-2",
			Name: "update_task_status",
			Args: json.RawMessage(`{"taskId":"1","status":"in_progress"}`),
		}}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{
			ID:   "call-3",
			Name: "update_task_status",
			Args: json.RawMessage(`{"taskId":"1","status":"completed"}`),
		}}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "The change is ready for review."}},
	)

	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithInstructions[struct{}, string](planning.Instructions),
		golem.WithTools[struct{}, string](plan...),
	)
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "Fix the failing test.")
	if err != nil {
		fmt.Println("Run:", err)
		return
	}

	fmt.Println("answer:", result.Output)
	fmt.Println("plan tool results:")
	for _, message := range result.Messages {
		if message.Role != model.RoleTool {
			continue
		}
		fmt.Printf("  %s:\n%s\n", message.ToolName, message.Content)
	}
}
