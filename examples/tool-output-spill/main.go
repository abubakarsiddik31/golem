// Command tool-output-spill keeps a large tool result losslessly: the
// model sees a marker, then reads the payload back through the generated
// read_tool_result tool. It runs offline against a scripted model.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
	"github.com/abubakarsiddik31/golem/tool"
)

func main() {
	full := strings.Repeat("full log line\n", 2000)
	readLog := tool.MustNew(tool.Tool[struct{}]{
		Name:        "read_log",
		Description: "Read the full build log.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(context.Context, struct{}, json.RawMessage) (tool.Result, error) {
			return tool.Text(full), nil
		},
	})

	store := tool.NewMemorySpill()
	client := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{
			ID:   "call-1",
			Name: "read_log",
			Args: json.RawMessage(`{}`),
		}}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "The log is spilled; I can read it in slices."}},
	)
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithTools[struct{}, string](readLog),
		golem.WithToolOutputSpill[struct{}, string](store, 200),
	)
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}
	if _, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "Read the build log."); err != nil {
		fmt.Println("Run:", err)
		return
	}

	requests := client.Requests()
	marker := ""
	for _, message := range requests[len(requests)-1].Messages {
		if message.Role == model.RoleTool {
			marker = message.Content
		}
	}
	fmt.Println(marker)
	handle := regexp.MustCompile(`handle "([^"]+)"`).FindStringSubmatch(marker)
	if len(handle) != 2 {
		return
	}

	client.Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{
			ID:   "call-2",
			Name: golem.SpillReadToolName,
			Args: json.RawMessage(`{"handle":"` + handle[1] + `","limit":28000}`),
		}}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "Read the full payload."}},
	)
	if _, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "Read it."); err != nil {
		fmt.Println("read run:", err)
		return
	}
	requests = client.Requests()
	read := ""
	for _, message := range requests[len(requests)-1].Messages {
		if message.Role == model.RoleTool && message.ToolName == golem.SpillReadToolName {
			read = message.Content
		}
	}
	fmt.Printf("read_tool_result restored %d bytes\n", len(read))
}
