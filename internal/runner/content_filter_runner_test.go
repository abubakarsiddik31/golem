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

func TestRejectContentFilterStopsBeforeTools(t *testing.T) {
	t.Parallel()

	executed := 0
	probe := tool.MustNew(tool.Tool[deps]{
		Name:        "probe",
		Description: "probe",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(context.Context, deps, json.RawMessage) (tool.Result, error) {
			executed++
			return tool.Text("ran"), nil
		},
	})
	spec := model.ToolSpec{Name: probe.Name, Description: probe.Description, Schema: probe.Schema}
	request := model.Request{
		Messages:  []model.Message{{Role: model.RoleUser, Content: "hi"}},
		ToolSpecs: []model.ToolSpec{spec},
	}
	client := &scriptedModel{responses: []model.Response{{
		Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "probe", Args: json.RawMessage(`{}`)},
		}},
		FinishReason: model.FinishContentFilter,
	}}}
	config := runner.ToolConfig{RejectContentFilter: true}
	outcome, err := runner.ExecuteWithToolConfig(context.Background(), client, []tool.Tool[deps]{probe},
		deps{}, request, 1, noRetries, config, "", nil, nil, runner.RunIdentity{})
	if !errors.Is(err, runner.ErrContentFilter) {
		t.Fatalf("error = %v, want ErrContentFilter", err)
	}
	if executed != 0 {
		t.Fatalf("probe ran %d times; the filter must stop before tools", executed)
	}
	if outcome.Response.FinishReason != model.FinishContentFilter {
		t.Fatalf("outcome finish reason = %q", outcome.Response.FinishReason)
	}
	if len(outcome.Messages) != 2 || outcome.Messages[1].Role != model.RoleAssistant {
		t.Fatalf("outcome messages = %#v, want request plus filtered response", outcome.Messages)
	}
}

func TestContentFilterPassesThroughWhenNotRejected(t *testing.T) {
	t.Parallel()

	client := &scriptedModel{responses: []model.Response{{
		Message:      model.Message{Role: model.RoleAssistant, Content: "partial"},
		FinishReason: model.FinishContentFilter,
	}}}
	request := model.Request{Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}}
	outcome, err := runner.ExecuteWithToolConfig(context.Background(), client, nil,
		deps{}, request, 1, noRetries, runner.ToolConfig{}, "", nil, nil, runner.RunIdentity{})
	if err != nil {
		t.Fatalf("ExecuteWithToolConfig() error = %v", err)
	}
	if outcome.Response.FinishReason != model.FinishContentFilter {
		t.Fatalf("finish reason = %q", outcome.Response.FinishReason)
	}
}
