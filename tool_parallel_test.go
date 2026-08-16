package golem_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

func TestAgentRunExecutesParallelToolsAndPreservesEvidenceOrder(t *testing.T) {
	entered := make(chan string, 2)
	release := make(chan struct{})
	makeTool := func(name string) tool.Tool[struct{}] {
		return tool.MustNew(tool.Tool[struct{}]{
			Name: name, Description: name, Schema: json.RawMessage(`{"type":"object"}`),
			Exec: func(ctx context.Context, _ struct{}, _ json.RawMessage) (string, error) {
				entered <- name
				select {
				case <-release:
					return name + " result", nil
				case <-ctx.Done():
					return "", ctx.Err()
				}
			},
		})
	}
	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "one", Name: "first", Args: json.RawMessage(`{}`)},
			{ID: "two", Name: "second", Args: json.RawMessage(`{}`)},
		}}},
		{Message: model.Message{Role: model.RoleAssistant, Content: "done"}},
	}}
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithTools[struct{}, string](makeTool("first"), makeTool("second")),
		golem.WithParallelToolCalls[struct{}, string]())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	type runResult struct {
		result golem.Result[string]
		err    error
	}
	finished := make(chan runResult, 1)
	go func() {
		result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
		finished <- runResult{result, err}
	}()
	<-entered
	<-entered
	close(release)
	run := <-finished
	if run.err != nil || run.result.Output != "done" {
		t.Fatalf("Run() = %#v, %v", run.result, run.err)
	}
	if run.result.Messages[2].ToolCallID != "one" || run.result.Messages[3].ToolCallID != "two" {
		t.Fatalf("tool evidence = %#v", run.result.Messages)
	}
}
