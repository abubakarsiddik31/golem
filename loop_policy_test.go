package golem_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem"
)

func TestLoopPolicyRepeatedResultStops(t *testing.T) {
	t.Parallel()

	executed := 0
	client := &queuedModel{responses: loopResponses(`{"job":"a"}`, `{"job":"a"}`, `{"job":"a"}`, `{"job":"a"}`)}
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithTools[struct{}, string](loopTool(t, &executed)),
		golem.WithLoopPolicy[struct{}, string](golem.LoopPolicy{RepeatedResult: 2}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	var loop *golem.LoopDetectedError
	if !errors.As(err, &loop) || loop.Pattern != golem.LoopRepeatedResult {
		t.Fatalf("Run() error = %v, want a repeated_result loop", err)
	}
	if executed != 3 {
		t.Fatalf("tool executed %d times, want three before the repeated result fires", executed)
	}
}

func TestLoopPolicyAlternatingCallsStopsBeforeExecution(t *testing.T) {
	t.Parallel()

	executed := 0
	client := &queuedModel{responses: loopResponses(`{"n":1}`, `{"n":2}`, `{"n":1}`, `{"n":2}`)}
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithTools[struct{}, string](loopTool(t, &executed)),
		golem.WithLoopPolicy[struct{}, string](golem.LoopPolicy{AlternatingCalls: 2}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	var loop *golem.LoopDetectedError
	if !errors.As(err, &loop) || loop.Pattern != golem.LoopAlternatingCalls {
		t.Fatalf("Run() error = %v, want an alternating_calls loop", err)
	}
	if executed != 3 {
		t.Fatalf("tool executed %d times, want the fourth call stopped before execution", executed)
	}
}

func TestLoopPolicyNudgesRepeatedCallAndContinues(t *testing.T) {
	t.Parallel()

	executed := 0
	client := &queuedModel{responses: loopResponses(`{"job":"a"}`, `{"job":"a"}`, `{"job":"a"}`)}
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithTools[struct{}, string](loopTool(t, &executed)),
		golem.WithLoopPolicy[struct{}, string](golem.LoopPolicy{RepeatedCall: 2, Action: golem.LoopNudge}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "done" || executed != 2 {
		t.Fatalf("result=%q executed=%d, want a completed run with two executions", result.Output, executed)
	}
	foundNudge := false
	for _, message := range result.Messages {
		if message.Role == "user" && strings.Contains(message.Content, "Change your approach") {
			foundNudge = true
		}
	}
	if !foundNudge {
		t.Fatalf("no nudge message in result: %#v", result.Messages)
	}
}

func TestLoopPolicyNudgesRepeatedResult(t *testing.T) {
	t.Parallel()

	executed := 0
	client := &queuedModel{responses: loopResponses(`{"job":"a"}`, `{"job":"a"}`, `{"job":"a"}`)}
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithTools[struct{}, string](loopTool(t, &executed)),
		golem.WithLoopPolicy[struct{}, string](golem.LoopPolicy{RepeatedResult: 2, Action: golem.LoopNudge}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "done" || executed != 3 {
		t.Fatalf("result=%q executed=%d", result.Output, executed)
	}
}

func TestLoopPolicyValidation(t *testing.T) {
	t.Parallel()

	cases := []golem.LoopPolicy{
		{RepeatedCall: 1},
		{Window: -1},
		{Action: 9},
	}
	for _, policy := range cases {
		if _, err := golem.New[struct{}, string](&fakeModel{}, decoderOf(),
			golem.WithLoopPolicy[struct{}, string](policy)); err == nil {
			t.Fatalf("policy %#v accepted", policy)
		}
	}
	if _, err := golem.New[struct{}, string](&fakeModel{}, decoderOf(),
		golem.WithLoopDetection[struct{}, string](2),
		golem.WithLoopPolicy[struct{}, string](golem.LoopPolicy{RepeatedCall: 2})); err == nil {
		t.Fatal("legacy and policy options accepted together")
	}
}
