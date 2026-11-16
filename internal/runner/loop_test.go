package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/abubakarsiddik31/golem/internal/runner"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

func TestLoopThresholdStopsTheRepeatedCall(t *testing.T) {
	t.Parallel()

	executed := 0
	poll := tool.MustNew(tool.Tool[deps]{
		Name:        "poll",
		Description: "poll",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(context.Context, deps, json.RawMessage) (tool.Result, error) {
			executed++
			return tool.Text("running"), nil
		},
	})
	spec := model.ToolSpec{Name: poll.Name, Description: poll.Description, Schema: poll.Schema}
	request := model.Request{
		Messages:  []model.Message{{Role: model.RoleUser, Content: "hi"}},
		ToolSpecs: []model.ToolSpec{spec},
	}
	call := func(id, args string) model.Response {
		return model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{
			ID:   id,
			Name: "poll",
			Args: json.RawMessage(args),
		}}}}
	}
	client := &scriptedModel{responses: []model.Response{
		call("call-1", `{"a":1,"b":2}`),
		call("call-2", `{"b":2,"a":1}`),
	}}
	config := runner.ToolConfig{LoopThreshold: 1}
	_, err := runner.ExecuteWithToolConfig(context.Background(), client, []tool.Tool[deps]{poll},
		deps{}, request, 3, noRetries, config, "", nil, nil, runner.RunIdentity{})
	var loop *runner.LoopError
	if !errors.As(err, &loop) {
		t.Fatalf("error = %v, want *runner.LoopError", err)
	}
	if loop.ToolName != "poll" || loop.Count != 2 || loop.Threshold != 1 {
		t.Fatalf("loop error = %#v", loop)
	}
	if executed != 1 {
		t.Fatalf("tool executed %d times, want one", executed)
	}
}
