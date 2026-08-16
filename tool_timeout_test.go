package golem_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

func TestAgentRunTimesOutToolWithItsContext(t *testing.T) {
	t.Parallel()

	wait := tool.MustNew(tool.Tool[struct{}]{
		Name:        "wait",
		Description: "Wait for cancellation.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Timeout:     time.Nanosecond,
		Exec: func(ctx context.Context, _ struct{}, _ json.RawMessage) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		},
	})
	client := &queuedModel{responses: []model.Response{{Message: model.Message{
		Role:      model.RoleAssistant,
		ToolCalls: []model.ToolCall{{ID: "call-1", Name: "wait", Args: json.RawMessage(`{}`)}},
	}}}}
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithTools[struct{}, string](wait),
		golem.WithToolTimeout[struct{}, string](time.Hour))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "wait")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v, want context.DeadlineExceeded", err)
	}
}
