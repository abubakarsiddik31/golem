package tool_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem/tool"
)

type deps struct{ Tenant string }

func validExec(context.Context, deps, json.RawMessage) (string, error) { return "", nil }

func TestNewRejectsInvalidToolDeclarations(t *testing.T) {
	t.Parallel()

	schema := json.RawMessage(`{"type":"object"}`)
	tests := []struct {
		name string
		tool tool.Tool[deps]
		want string
	}{
		{
			name: "missing name",
			tool: tool.Tool[deps]{Description: "d", Schema: schema, Exec: validExec},
			want: "name is required",
		},
		{
			name: "missing schema",
			tool: tool.Tool[deps]{Name: "t", Description: "d", Exec: validExec},
			want: "schema is required",
		},
		{
			name: "invalid schema json",
			tool: tool.Tool[deps]{Name: "t", Description: "d", Schema: json.RawMessage(`{`), Exec: validExec},
			want: "not valid JSON",
		},
		{
			name: "missing exec",
			tool: tool.Tool[deps]{Name: "t", Description: "d", Schema: schema},
			want: "exec function is required",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := tool.New(test.tool)
			if err == nil {
				t.Fatal("New() error = nil, want rejection")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("New() error = %q, want it to mention %q", err, test.want)
			}
		})
	}
}

func TestMetadataIsInspectableWithoutExecution(t *testing.T) {
	t.Parallel()

	var executions int
	roll, err := tool.New(tool.Tool[deps]{
		Name:        "roll_dice",
		Description: "Roll a six-sided die.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"guess":{"type":"integer"}}}`),
		Exec: func(context.Context, deps, json.RawMessage) (string, error) {
			executions++
			return "", nil
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if roll.Name != "roll_dice" || roll.Description != "Roll a six-sided die." {
		t.Fatalf("tool metadata = %#v", roll)
	}
	if !json.Valid(roll.Schema) {
		t.Fatalf("Schema = %s, want valid JSON", roll.Schema)
	}
	if executions != 0 {
		t.Fatalf("executions = %d, want 0: metadata inspection must not execute", executions)
	}
}

func TestExecReceivesContextDepsAndRawArgs(t *testing.T) {
	t.Parallel()

	type capture struct {
		ctx  context.Context
		deps deps
		args json.RawMessage
	}
	var got capture

	echo, err := tool.New(tool.Tool[deps]{
		Name:        "echo",
		Description: "Echo the tenant.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, d deps, args json.RawMessage) (string, error) {
			got = capture{ctx: ctx, deps: d, args: args}
			return "ok", nil
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx := context.WithValue(context.Background(), contextKey("request-id"), "run-42")
	rawArgs := json.RawMessage(`{"guess":4}`)
	output, err := echo.Exec(ctx, deps{Tenant: "acme"}, rawArgs)
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if output != "ok" {
		t.Fatalf("Exec() output = %q, want ok", output)
	}
	if got.ctx != ctx {
		t.Fatal("Exec() did not receive the caller's context")
	}
	if got.deps.Tenant != "acme" {
		t.Fatalf("Exec() deps = %#v, want tenant acme", got.deps)
	}
	if string(got.args) != `{"guess":4}` {
		t.Fatalf("Exec() args = %s, want raw JSON unchanged", got.args)
	}
}

func TestExecFailureIsReturnedNotSwallowed(t *testing.T) {
	t.Parallel()

	cause := errors.New("database unavailable")
	failing, err := tool.New(tool.Tool[deps]{
		Name:        "failing",
		Description: "Always fails.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(context.Context, deps, json.RawMessage) (string, error) {
			return "", cause
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, err := failing.Exec(context.Background(), deps{}, nil); !errors.Is(err, cause) {
		t.Fatalf("Exec() error = %v, want cause %v", err, cause)
	}
}

type contextKey string

func (k contextKey) String() string { return string(k) }
