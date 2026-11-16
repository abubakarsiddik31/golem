// Command loop-detection stops a run that keeps polling the same tool
// with identical arguments, before the repeated call executes. It runs
// offline against a scripted model.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
	"github.com/abubakarsiddik31/golem/tool"
)

func main() {
	poll := tool.MustNew(tool.Tool[struct{}]{
		Name:        "poll_job",
		Description: "Check whether the background job finished.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"jobId":{"type":"string"}},"required":["jobId"]}`),
		Exec: func(context.Context, struct{}, json.RawMessage) (tool.Result, error) {
			return tool.Text("still running"), nil
		},
	})

	call := func(id string) model.Response {
		return model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{
			ID:   id,
			Name: "poll_job",
			Args: json.RawMessage(`{"jobId":"job-42"}`),
		}}}}
	}
	client := testmodel.New().Respond(call("call-1"), call("call-2"), call("call-3"), call("call-4"))
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithTools[struct{}, string](poll),
		golem.WithLoopDetection[struct{}, string](3),
	)
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "Wait for job-42.")
	var runErr *golem.RunError
	var loop *golem.LoopDetectedError
	if errors.As(err, &runErr) && errors.As(err, &loop) {
		fmt.Printf("stopped at %s: %s repeated %d times (limit %d)\n",
			runErr.Stage, loop.ToolName, loop.Count, loop.Threshold)
		return
	}
	fmt.Println("run:", err)
}
