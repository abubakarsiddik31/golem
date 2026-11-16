package runner_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem/internal/runner"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

func runnerTool(t *testing.T, name string) tool.Tool[deps] {
	t.Helper()
	return tool.MustNew(tool.Tool[deps]{
		Name:        name,
		Description: name,
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(context.Context, deps, json.RawMessage) (tool.Result, error) {
			return tool.Text("ok"), nil
		},
	})
}

func prepareRequest(specs ...model.ToolSpec) model.Request {
	return model.Request{
		Messages:  []model.Message{{Role: model.RoleUser, Content: "hi"}},
		ToolSpecs: specs,
	}
}

func TestPrepareFiltersSpecsPerRequest(t *testing.T) {
	t.Parallel()

	read := runnerTool(t, "read_file")
	del := runnerTool(t, "delete_file")
	specs := []model.ToolSpec{
		{Name: read.Name, Description: read.Description, Schema: read.Schema},
		{Name: del.Name, Description: del.Description, Schema: del.Schema},
	}
	client := &scriptedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "read_file", Args: json.RawMessage(`{}`)},
		}}},
		textResponse("done", model.Usage{}),
	}}
	turn := 0
	config := runner.ToolConfig{
		Prepare: func(_ context.Context, advertised []model.ToolSpec) ([]model.ToolSpec, error) {
			defer func() { turn++ }()
			if turn == 1 {
				return []model.ToolSpec{advertised[1]}, nil
			}
			return []model.ToolSpec{advertised[0]}, nil
		},
	}
	if _, err := runner.ExecuteWithToolConfig(context.Background(), client, []tool.Tool[deps]{read, del},
		deps{}, prepareRequest(specs...), 3, noRetries, config, "", nil, nil, runner.RunIdentity{}); err != nil {
		t.Fatalf("ExecuteWithToolConfig() error = %v", err)
	}
	if len(client.requests) != 2 {
		t.Fatalf("model requests = %d, want 2", len(client.requests))
	}
	if got := client.requests[0].ToolSpecs[0].Name; got != "read_file" {
		t.Fatalf("first request tool = %q", got)
	}
	if got := client.requests[1].ToolSpecs[0].Name; got != "delete_file" {
		t.Fatalf("second request tool = %q", got)
	}
}

func TestPrepareRewritesADescription(t *testing.T) {
	t.Parallel()

	read := runnerTool(t, "read_file")
	specs := []model.ToolSpec{{Name: read.Name, Description: "old", Schema: read.Schema}}
	client := &scriptedModel{responses: []model.Response{textResponse("done", model.Usage{})}}
	config := runner.ToolConfig{
		Prepare: func(_ context.Context, advertised []model.ToolSpec) ([]model.ToolSpec, error) {
			advertised[0].Description = "new"
			return advertised, nil
		},
	}
	if _, err := runner.ExecuteWithToolConfig(context.Background(), client, []tool.Tool[deps]{read},
		deps{}, prepareRequest(specs...), 1, noRetries, config, "", nil, nil, runner.RunIdentity{}); err != nil {
		t.Fatalf("ExecuteWithToolConfig() error = %v", err)
	}
	if got := client.requests[0].ToolSpecs[0].Description; got != "new" {
		t.Fatalf("advertised description = %q", got)
	}
	if specs[0].Description != "old" {
		t.Fatal("preparation mutated the caller's request specs")
	}
}

func TestPrepareRejectsInvalidSpecsBeforeTheModel(t *testing.T) {
	t.Parallel()

	read := runnerTool(t, "read_file")
	specs := []model.ToolSpec{{Name: read.Name, Description: read.Description, Schema: read.Schema}}
	cases := []struct {
		name    string
		prepare func(context.Context, []model.ToolSpec) ([]model.ToolSpec, error)
		want    string
	}{
		{
			name: "error",
			prepare: func(context.Context, []model.ToolSpec) ([]model.ToolSpec, error) {
				return nil, context.Canceled
			},
			want: "context canceled",
		},
		{
			name: "undeclared",
			prepare: func(_ context.Context, _ []model.ToolSpec) ([]model.ToolSpec, error) {
				return []model.ToolSpec{{Name: "ghost", Schema: json.RawMessage(`{}`)}}, nil
			},
			want: "not declared",
		},
		{
			name: "duplicate",
			prepare: func(_ context.Context, advertised []model.ToolSpec) ([]model.ToolSpec, error) {
				return []model.ToolSpec{advertised[0], advertised[0]}, nil
			},
			want: "more than once",
		},
		{
			name: "bad schema",
			prepare: func(_ context.Context, advertised []model.ToolSpec) ([]model.ToolSpec, error) {
				advertised[0].Schema = json.RawMessage(`{`)
				return advertised, nil
			},
			want: "valid schema",
		},
		{
			name: "empty name",
			prepare: func(context.Context, []model.ToolSpec) ([]model.ToolSpec, error) {
				return []model.ToolSpec{{Schema: json.RawMessage(`{}`)}}, nil
			},
			want: "empty name",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &scriptedModel{}
			config := runner.ToolConfig{Prepare: tc.prepare}
			_, err := runner.ExecuteWithToolConfig(context.Background(), client, []tool.Tool[deps]{read},
				deps{}, prepareRequest(specs...), 1, noRetries, config, "", nil, nil, runner.RunIdentity{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			if len(client.requests) != 0 {
				t.Fatal("model was called despite invalid prepared specs")
			}
		})
	}
}

func TestPrepareKeepsTheOutputTool(t *testing.T) {
	t.Parallel()

	read := runnerTool(t, "read_file")
	answer := model.ToolSpec{Name: "answer", Schema: json.RawMessage(`{"type":"object"}`)}
	specs := []model.ToolSpec{
		{Name: read.Name, Description: read.Description, Schema: read.Schema},
		answer,
	}
	client := &scriptedModel{}
	config := runner.ToolConfig{
		Prepare: func(_ context.Context, advertised []model.ToolSpec) ([]model.ToolSpec, error) {
			return advertised[:1], nil
		},
	}
	_, err := runner.ExecuteWithToolConfig(context.Background(), client, []tool.Tool[deps]{read},
		deps{}, prepareRequest(specs...), 1, noRetries, config, "answer", nil, nil, runner.RunIdentity{})
	if err == nil || !strings.Contains(err.Error(), `removed the output tool "answer"`) {
		t.Fatalf("error = %v, want the missing output tool", err)
	}
	if len(client.requests) != 0 {
		t.Fatal("model was called despite the missing output tool")
	}
}
