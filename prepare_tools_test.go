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

func prepareTool[Deps any](t *testing.T, name string) tool.Tool[Deps] {
	t.Helper()
	return tool.MustNew(tool.Tool[Deps]{
		Name:        name,
		Description: name + " description",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(context.Context, Deps, json.RawMessage) (tool.Result, error) {
			return tool.Text("ok"), nil
		},
	})
}

func specNames(specs []model.ToolSpec) []string {
	names := make([]string, len(specs))
	for i, spec := range specs {
		names[i] = spec.Name
	}
	return names
}

func TestPrepareToolsFiltersEachStep(t *testing.T) {
	t.Parallel()

	read := prepareTool[struct{ Role string }](t, "read_file")
	del := prepareTool[struct{ Role string }](t, "delete_file")
	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "read_file", Args: json.RawMessage(`{}`)},
		}}},
		{Message: model.Message{Role: model.RoleAssistant, Content: "done"}},
	}}

	turn := 0
	var sawDeps string
	agent, err := golem.New[struct{ Role string }, string](client, decoderOf(),
		golem.WithTools[struct{ Role string }, string](read, del),
		golem.WithPrepareTools[struct{ Role string }, string](func(_ context.Context, runCtx golem.RunContext[struct{ Role string }], specs []model.ToolSpec) ([]model.ToolSpec, error) {
			sawDeps = runCtx.Deps.Role
			defer func() { turn++ }()
			if turn == 0 {
				return keepSpecs(specs, "read_file"), nil
			}
			return keepSpecs(specs, "delete_file"), nil
		}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, err := agent.Run(context.Background(), golem.RunContext[struct{ Role string }]{
		Deps: struct{ Role string }{Role: "reader"},
	}, "go"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if sawDeps != "reader" {
		t.Fatalf("prepare saw deps %q, want the run's typed deps", sawDeps)
	}
	want := [][]string{{"read_file"}, {"delete_file"}}
	for i, request := range client.requests {
		if got := specNames(request.ToolSpecs); !equalStrings(got, want[i]) {
			t.Fatalf("request %d tools = %v, want %v", i, got, want[i])
		}
	}
}

func TestPrepareToolsRewritesADescription(t *testing.T) {
	t.Parallel()

	read := prepareTool[struct{}](t, "read_file")
	client := &fakeModel{response: model.Response{
		Message: model.Message{Role: model.RoleAssistant, Content: "done"},
	}}
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithTools[struct{}, string](read),
		golem.WithPrepareTools[struct{}, string](func(_ context.Context, _ golem.RunContext[struct{}], specs []model.ToolSpec) ([]model.ToolSpec, error) {
			rewritten := append([]model.ToolSpec(nil), specs...)
			rewritten[0].Description = "read a file from the caller's workspace"
			return rewritten, nil
		}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := client.request.ToolSpecs[0].Description; got != "read a file from the caller's workspace" {
		t.Fatalf("advertised description = %q", got)
	}
}

func TestPrepareToolsRejectsInvalidSpecsBeforeTheModel(t *testing.T) {
	t.Parallel()

	read := prepareTool[struct{}](t, "read_file")
	cases := []struct {
		name    string
		prepare golem.PrepareToolsFunc[struct{}]
		want    string
	}{
		{
			name: "undeclared tool",
			prepare: func(_ context.Context, _ golem.RunContext[struct{}], specs []model.ToolSpec) ([]model.ToolSpec, error) {
				return append(specs, model.ToolSpec{Name: "ghost", Schema: json.RawMessage(`{"type":"object"}`)}), nil
			},
			want: "not declared",
		},
		{
			name: "duplicate name",
			prepare: func(_ context.Context, _ golem.RunContext[struct{}], specs []model.ToolSpec) ([]model.ToolSpec, error) {
				return append(specs, specs[0]), nil
			},
			want: "more than once",
		},
		{
			name: "invalid schema",
			prepare: func(_ context.Context, _ golem.RunContext[struct{}], specs []model.ToolSpec) ([]model.ToolSpec, error) {
				rewritten := append([]model.ToolSpec(nil), specs...)
				rewritten[0].Schema = json.RawMessage(`{`)
				return rewritten, nil
			},
			want: "valid schema",
		},
		{
			name: "empty name",
			prepare: func(_ context.Context, _ golem.RunContext[struct{}], _ []model.ToolSpec) ([]model.ToolSpec, error) {
				return []model.ToolSpec{{Schema: json.RawMessage(`{"type":"object"}`)}}, nil
			},
			want: "empty name",
		},
		{
			name: "prepare error",
			prepare: func(_ context.Context, _ golem.RunContext[struct{}], _ []model.ToolSpec) ([]model.ToolSpec, error) {
				return nil, errors.New("no tools for this user")
			},
			want: "no tools for this user",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeModel{}
			agent, err := golem.New[struct{}, string](client, decoderOf(),
				golem.WithTools[struct{}, string](read),
				golem.WithPrepareTools[struct{}, string](tc.prepare),
			)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Run() error = %v, want %q", err, tc.want)
			}
			if client.request.Messages != nil {
				t.Fatal("model was called despite invalid prepared specs")
			}
		})
	}
}

func TestPrepareToolsCannotRemoveTheOutputTool(t *testing.T) {
	t.Parallel()

	read := prepareTool[struct{}](t, "read_file")
	client := &fakeModel{}
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithTools[struct{}, string](read),
		golem.WithOutputTool[struct{}, string]("answer", "", json.RawMessage(`{"type":"object"}`)),
		golem.WithPrepareTools[struct{}, string](func(_ context.Context, _ golem.RunContext[struct{}], specs []model.ToolSpec) ([]model.ToolSpec, error) {
			return keepSpecs(specs, "read_file"), nil
		}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	if err == nil || !strings.Contains(err.Error(), `removed the output tool "answer"`) {
		t.Fatalf("Run() error = %v, want the missing output tool", err)
	}
	if client.request.Messages != nil {
		t.Fatal("model was called despite the missing output tool")
	}
}

func keepSpecs(specs []model.ToolSpec, names ...string) []model.ToolSpec {
	keep := make(map[string]struct{}, len(names))
	for _, name := range names {
		keep[name] = struct{}{}
	}
	kept := make([]model.ToolSpec, 0, len(names))
	for _, spec := range specs {
		if _, ok := keep[spec.Name]; ok {
			kept = append(kept, spec)
		}
	}
	return kept
}
