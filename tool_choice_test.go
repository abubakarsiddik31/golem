package golem_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

func TestWithToolChoiceAdvertisesOnlyChosenTool(t *testing.T) {
	t.Parallel()
	makeTool := func(name string) tool.Tool[struct{}] {
		return tool.MustNew(tool.Tool[struct{}]{Name: name, Description: name, Schema: json.RawMessage(`{}`), Exec: func(context.Context, struct{}, json.RawMessage) (string, error) { return "", nil }})
	}
	client := &queuedModel{responses: []model.Response{{Message: model.Message{Role: model.RoleAssistant, Content: "done"}}}}
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithTools[struct{}, string](makeTool("first"), makeTool("chosen")),
		golem.WithToolChoice[struct{}, string]("chosen"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := client.requests[0].ToolSpecs; len(got) != 1 || got[0].Name != "chosen" {
		t.Fatalf("ToolSpecs = %#v", got)
	}
}

func TestWithToolChoiceRejectsUnregisteredName(t *testing.T) {
	t.Parallel()
	if _, err := golem.New[struct{}, string](&queuedModel{}, decoderOf(), golem.WithToolChoice[struct{}, string]("missing")); err == nil {
		t.Fatal("New() error = nil, want unregistered choice rejection")
	}
}
