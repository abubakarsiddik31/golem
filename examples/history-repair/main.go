// Command history-repair normalizes a damaged conversation the way an
// application boundary would: a crashed run left a tool call without a
// result, a pipeline dropped its call, and a stream died mid-arguments.
// golem.NormalizeHistory repairs the pairing, reports everything it did,
// and names the truncated arguments it detected — while keeping the raw
// bytes verbatim.
//
// The run is fully scripted — no network, no credentials. Adapters make
// truncated arguments sendable on the wire as {"truncated_args": ...};
// the stored history keeps the raw value.
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
)

func main() {
	// Damaged evidence: call-1 answered, call-2 never got a result and
	// its arguments were cut off mid-stream, and one result's call is
	// missing entirely.
	history := []model.Message{
		{Role: model.RoleUser, Content: "roll twice"},
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "roll", Args: json.RawMessage(`{"sides":6}`)},
			{ID: "call-2", Name: "roll", Args: json.RawMessage(`{"sides":`)},
		}},
		{Role: model.RoleTool, ToolCallID: "call-1", ToolName: "roll", Content: "4"},
		{Role: model.RoleTool, ToolCallID: "ghost", ToolName: "roll", Content: "orphaned"},
	}

	normalized, report := golem.NormalizeHistory(history)
	fmt.Printf("synthesized results for: %v\n", report.Synthesized)
	fmt.Printf("dropped orphaned results for: %v\n", report.Dropped)
	fmt.Printf("truncated arguments on: %v (bytes kept verbatim: %s)\n",
		report.Truncated, normalized[1].ToolCalls[1].Args)

	// A damaged history never blocks a run: the repaired conversation
	// continues against a scripted model.
	client := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant,
			Content: "picking up where the crash left off."}})
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) {
			return r.Message.Content, nil
		}))
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}
	result, err := agent.RunWithHistory(context.Background(), golem.RunContext[struct{}]{},
		normalized, "go on")
	if err != nil {
		fmt.Println("RunWithHistory:", err)
		return
	}
	fmt.Println("output:", result.Output)
}
