package golem_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

func TestContentFilterErrorOptionFailsWithEvidence(t *testing.T) {
	t.Parallel()

	client := &fakeModel{response: model.Response{
		Message:      model.Message{Role: model.RoleAssistant, Content: "I cannot help with that."},
		FinishReason: model.FinishContentFilter,
	}}
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithContentFilterError[struct{}, string]())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "prompt")
	var runErr *golem.RunError
	if !errors.As(err, &runErr) {
		t.Fatalf("Run() error = %v, want *RunError", err)
	}
	if runErr.Stage != golem.StageContentFilter {
		t.Fatalf("stage = %q, want %q", runErr.Stage, golem.StageContentFilter)
	}
	var filterErr *golem.ContentFilterError
	if !errors.As(err, &filterErr) {
		t.Fatalf("error = %v, want *ContentFilterError in the chain", err)
	}
	if filterErr.Message.Content != "I cannot help with that." {
		t.Fatalf("filtered message = %#v", filterErr.Message)
	}
	if runErr.Partial == nil || len(runErr.Partial.Messages) == 0 {
		t.Fatalf("partial evidence = %#v, want the filtered response", runErr.Partial)
	}
	last := runErr.Partial.Messages[len(runErr.Partial.Messages)-1]
	if last.Role != model.RoleAssistant || last.Content != "I cannot help with that." {
		t.Fatalf("last partial message = %#v", last)
	}
}

func TestContentFilterWithoutOptionIsOrdinaryOutput(t *testing.T) {
	t.Parallel()

	client := &fakeModel{response: model.Response{
		Message:      model.Message{Role: model.RoleAssistant, Content: "partial answer"},
		FinishReason: model.FinishContentFilter,
	}}
	agent, err := golem.New[struct{}, string](client, decoderOf())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "prompt")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "partial answer" || result.FinishReason != model.FinishContentFilter {
		t.Fatalf("result = %#v", result)
	}
}

func TestContentFilterStopsToolExecution(t *testing.T) {
	t.Parallel()

	executed := 0
	probe := tool.MustNew(tool.Tool[struct{}]{
		Name:        "probe",
		Description: "probe",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(context.Context, struct{}, json.RawMessage) (tool.Result, error) {
			executed++
			return tool.Text("ran"), nil
		},
	})
	client := &fakeModel{response: model.Response{
		Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "probe", Args: json.RawMessage(`{}`)},
		}},
		FinishReason: model.FinishContentFilter,
	}}
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithTools[struct{}, string](probe),
		golem.WithContentFilterError[struct{}, string]())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "prompt")
	var filterErr *golem.ContentFilterError
	if !errors.As(err, &filterErr) {
		t.Fatalf("Run() error = %v, want *ContentFilterError", err)
	}
	if executed != 0 {
		t.Fatalf("probe ran %d times; a filtered response must stop before tools", executed)
	}
}

func TestContentFilterEmptyResponseStillFails(t *testing.T) {
	t.Parallel()

	client := &fakeModel{response: model.Response{
		Message:      model.Message{Role: model.RoleAssistant},
		FinishReason: model.FinishContentFilter,
	}}
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithContentFilterError[struct{}, string]())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "prompt")
	var filterErr *golem.ContentFilterError
	if !errors.As(err, &filterErr) {
		t.Fatalf("Run() error = %v, want *ContentFilterError", err)
	}
	if filterErr.Message.Content != "" {
		t.Fatalf("filtered message = %#v", filterErr.Message)
	}
}
