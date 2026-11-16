package golem_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

func loopTool(t *testing.T, executed *int) tool.Tool[struct{}] {
	t.Helper()
	return tool.MustNew(tool.Tool[struct{}]{
		Name:        "poll",
		Description: "poll",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(context.Context, struct{}, json.RawMessage) (tool.Result, error) {
			*executed++
			return tool.Text("still running"), nil
		},
	})
}

func loopResponses(args ...string) []model.Response {
	responses := make([]model.Response, 0, len(args)+1)
	for i, arg := range args {
		responses = append(responses, model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{
			ID:   "call-" + string(rune('1'+i)),
			Name: "poll",
			Args: json.RawMessage(arg),
		}}}})
	}
	return append(responses, model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "done"}})
}

func TestLoopDetectionStopsBeforeTheRepeatedCall(t *testing.T) {
	t.Parallel()

	executed := 0
	client := &queuedModel{responses: loopResponses(`{"job":"a"}`, `{"job":"a"}`, `{"job":"a"}`)}
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithTools[struct{}, string](loopTool(t, &executed)),
		golem.WithLoopDetection[struct{}, string](2),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageLoopDetected {
		t.Fatalf("Run() error = %v, want StageLoopDetected", err)
	}
	var loop *golem.LoopDetectedError
	if !errors.As(err, &loop) {
		t.Fatalf("Run() error = %v, want *LoopDetectedError", err)
	}
	if loop.ToolName != "poll" || loop.Count != 3 || loop.Threshold != 2 || string(loop.Args) != `{"job":"a"}` {
		t.Fatalf("loop error = %#v", loop)
	}
	if executed != 2 {
		t.Fatalf("tool executed %d times, want the two allowed repeats", executed)
	}
	if runErr.Partial == nil || len(runErr.Partial.Messages) == 0 {
		t.Fatal("loop failure lost partial evidence")
	}
}

func TestLoopDetectionCanonicalizesArguments(t *testing.T) {
	t.Parallel()

	executed := 0
	client := &queuedModel{responses: loopResponses(`{"a":1,"b":2}`, `{"b":2,"a":1}`)}
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithTools[struct{}, string](loopTool(t, &executed)),
		golem.WithLoopDetection[struct{}, string](1),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	var loop *golem.LoopDetectedError
	if !errors.As(err, &loop) {
		t.Fatalf("Run() error = %v, want *LoopDetectedError", err)
	}
	if executed != 1 {
		t.Fatalf("tool executed %d times, want one before the key-order repeat", executed)
	}
}

func TestLoopDetectionAllowsDistinctAndBoundedCalls(t *testing.T) {
	t.Parallel()

	executed := 0
	client := &queuedModel{responses: loopResponses(`{"job":"a"}`, `{"job":"b"}`, `{"job":"c"}`)}
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithTools[struct{}, string](loopTool(t, &executed)),
		golem.WithLoopDetection[struct{}, string](1),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if executed != 3 {
		t.Fatalf("tool executed %d times, want 3 distinct calls", executed)
	}

	executed = 0
	client = &queuedModel{responses: loopResponses(`{"job":"a"}`, `{"job":"a"}`, `{"job":"a"}`)}
	agent, err = golem.New[struct{}, string](client, decoderOf(),
		golem.WithTools[struct{}, string](loopTool(t, &executed)),
		golem.WithLoopDetection[struct{}, string](3),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if executed != 3 {
		t.Fatalf("tool executed %d times, want the configured three", executed)
	}
}

func TestLoopDetectionDisabledAndValidated(t *testing.T) {
	t.Parallel()

	if _, err := golem.New[struct{}, string](&fakeModel{}, decoderOf(),
		golem.WithLoopDetection[struct{}, string](-1)); err == nil {
		t.Fatal("negative loop threshold accepted")
	}

	executed := 0
	args := []string{`{"job":"a"}`, `{"job":"a"}`, `{"job":"a"}`, `{"job":"a"}`}
	client := &queuedModel{responses: loopResponses(args...)}
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithTools[struct{}, string](loopTool(t, &executed)),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if executed != 4 {
		t.Fatalf("disabled detection executed %d times, want 4", executed)
	}
}

func TestLoopDetectionMalformedArgumentsStillCompare(t *testing.T) {
	t.Parallel()

	executed := 0
	client := &queuedModel{responses: loopResponses(`{"truncated":`, `{"truncated":`)}
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithTools[struct{}, string](loopTool(t, &executed)),
		golem.WithLoopDetection[struct{}, string](1),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	if !strings.Contains(err.Error(), "repeated") {
		t.Fatalf("Run() error = %v, want a repeated-call failure", err)
	}
}
