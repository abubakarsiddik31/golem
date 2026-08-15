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

type deps struct{ Tenant string }

// scriptedModel returns queued responses in order and records every request.
type scriptedModel struct {
	requests  []model.Request
	responses []model.Response
	err       error
}

func (m *scriptedModel) Generate(ctx context.Context, request model.Request) (model.Response, error) {
	m.requests = append(m.requests, request)
	if m.err != nil {
		return model.Response{}, m.err
	}
	if len(m.responses) == 0 {
		return model.Response{}, errors.New("scriptedModel: no queued response")
	}
	response := m.responses[0]
	m.responses = m.responses[1:]
	return response, nil
}

func textResponse(content string, usage model.Usage) model.Response {
	return model.Response{
		Message: model.Message{Role: model.RoleAssistant, Content: content},
		Usage:   usage,
	}
}

func toolResponse(calls ...model.ToolCall) model.Response {
	return model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: calls}}
}

func echoTool(t *testing.T) tool.Tool[deps] {
	t.Helper()
	return tool.MustNew(tool.Tool[deps]{
		Name:        "echo",
		Description: "Echo the tenant and raw args.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, d deps, args json.RawMessage) (string, error) {
			return d.Tenant + " " + string(args), nil
		},
	})
}

func specsFor(t *testing.T, tools ...tool.Tool[deps]) []model.ToolSpec {
	t.Helper()
	specs := make([]model.ToolSpec, 0, len(tools))
	for _, tl := range tools {
		specs = append(specs, model.ToolSpec{Name: tl.Name, Description: tl.Description, Schema: tl.Schema})
	}
	return specs
}

func TestExecuteReturnsFinalResponseWithoutToolCalls(t *testing.T) {
	t.Parallel()

	m := &scriptedModel{responses: []model.Response{
		textResponse("42", model.Usage{InputTokens: 10, OutputTokens: 2}),
	}}
	outcome, err := runner.Execute(context.Background(), m, nil, deps{},
		model.Request{Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}}, 1)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if outcome.Response.Message.Content != "42" {
		t.Fatalf("final content = %q", outcome.Response.Message.Content)
	}
	if len(outcome.Messages) != 2 || outcome.Messages[0].Role != model.RoleUser || outcome.Messages[1].Role != model.RoleAssistant {
		t.Fatalf("evidence = %#v", outcome.Messages)
	}
	if outcome.Usage != (model.Usage{InputTokens: 10, OutputTokens: 2}) {
		t.Fatalf("usage = %#v", outcome.Usage)
	}
}

func TestExecutePerformsOneToolRoundTrip(t *testing.T) {
	t.Parallel()

	arguments := json.RawMessage(`{"guess":4}`)
	m := &scriptedModel{responses: []model.Response{
		toolResponse(model.ToolCall{ID: "call-1", Name: "echo", Args: arguments}),
		textResponse("done", model.Usage{InputTokens: 20, OutputTokens: 3}),
	}}
	echo := echoTool(t)

	outcome, err := runner.Execute(context.Background(), m, []tool.Tool[deps]{echo}, deps{Tenant: "acme"},
		model.Request{
			Messages:  []model.Message{{Role: model.RoleUser, Content: "roll"}},
			ToolSpecs: specsFor(t, echo),
		}, 5)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if outcome.Response.Message.Content != "done" {
		t.Fatalf("final content = %q", outcome.Response.Message.Content)
	}
	if outcome.Usage != (model.Usage{InputTokens: 20, OutputTokens: 3}) {
		t.Fatalf("usage = %#v", outcome.Usage)
	}

	// The second model call must see the tool result in order.
	second := m.requests[1]
	if len(second.Messages) != 3 {
		t.Fatalf("second request messages = %#v", second.Messages)
	}
	result := second.Messages[2]
	if result.Role != model.RoleTool || result.ToolCallID != "call-1" || result.ToolName != "echo" {
		t.Fatalf("tool result message = %#v", result)
	}
	if result.Content != "acme {\"guess\":4}" {
		t.Fatalf("tool result content = %q", result.Content)
	}

	// Full evidence, in execution order.
	wantRoles := []model.Role{model.RoleUser, model.RoleAssistant, model.RoleTool, model.RoleAssistant}
	if len(outcome.Messages) != len(wantRoles) {
		t.Fatalf("evidence length = %d, want %d", len(outcome.Messages), len(wantRoles))
	}
	for i, role := range wantRoles {
		if outcome.Messages[i].Role != role {
			t.Fatalf("evidence[%d].Role = %q, want %q", i, outcome.Messages[i].Role, role)
		}
	}
}

func TestExecuteRunsMultipleToolRoundsUntilFinalResponse(t *testing.T) {
	t.Parallel()

	m := &scriptedModel{responses: []model.Response{
		toolResponse(model.ToolCall{ID: "c1", Name: "echo", Args: json.RawMessage(`{}`)}),
		toolResponse(model.ToolCall{ID: "c2", Name: "echo", Args: json.RawMessage(`{}`)}),
		textResponse("settled", model.Usage{}),
	}}
	echo := echoTool(t)

	outcome, err := runner.Execute(context.Background(), m, []tool.Tool[deps]{echo}, deps{Tenant: "acme"},
		model.Request{
			Messages:  []model.Message{{Role: model.RoleUser, Content: "twice"}},
			ToolSpecs: specsFor(t, echo),
		}, 5)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(m.requests) != 3 {
		t.Fatalf("model turns = %d, want 3", len(m.requests))
	}
	// user, assistant-call, tool, assistant-call, tool, assistant-final
	if len(outcome.Messages) != 6 {
		t.Fatalf("evidence length = %d, want 6: %#v", len(outcome.Messages), outcome.Messages)
	}
}

func TestExecuteAbortsWhenTurnLimitIsExceeded(t *testing.T) {
	t.Parallel()

	m := &scriptedModel{responses: []model.Response{
		toolResponse(model.ToolCall{ID: "c1", Name: "echo", Args: json.RawMessage(`{}`)}),
		toolResponse(model.ToolCall{ID: "c2", Name: "echo", Args: json.RawMessage(`{}`)}),
	}}
	echo := echoTool(t)

	_, err := runner.Execute(context.Background(), m, []tool.Tool[deps]{echo}, deps{},
		model.Request{ToolSpecs: specsFor(t, echo)}, 2)
	if !errors.Is(err, runner.ErrLoopLimit) {
		t.Fatalf("Execute() error = %v, want ErrLoopLimit", err)
	}
	if len(m.requests) != 2 {
		t.Fatalf("model turns = %d, want exactly the limit", len(m.requests))
	}
}

func TestExecuteClassifiesToolFailures(t *testing.T) {
	t.Parallel()

	cause := errors.New("database unavailable")
	failing := tool.MustNew(tool.Tool[deps]{
		Name:        "failing",
		Description: "Always fails.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(context.Context, deps, json.RawMessage) (string, error) {
			return "", cause
		},
	})
	m := &scriptedModel{responses: []model.Response{
		toolResponse(model.ToolCall{ID: "call-9", Name: "failing", Args: json.RawMessage(`{}`)}),
	}}

	_, err := runner.Execute(context.Background(), m, []tool.Tool[deps]{failing}, deps{},
		model.Request{ToolSpecs: specsFor(t, failing)}, 3)
	var toolErr *runner.ToolError
	if !errors.As(err, &toolErr) {
		t.Fatalf("Execute() error = %v, want ToolError", err)
	}
	if toolErr.ToolName != "failing" || toolErr.CallID != "call-9" {
		t.Fatalf("ToolError = %#v", toolErr)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("Execute() error = %v, want cause %v", err, cause)
	}
}

func TestExecuteRejectsModelRequestsForUndeclaredTools(t *testing.T) {
	t.Parallel()

	echo := echoTool(t)
	m := &scriptedModel{responses: []model.Response{
		toolResponse(model.ToolCall{ID: "call-1", Name: "unknown_tool", Args: json.RawMessage(`{}`)}),
	}}

	_, err := runner.Execute(context.Background(), m, []tool.Tool[deps]{echo}, deps{},
		model.Request{ToolSpecs: specsFor(t, echo)}, 3)
	var toolErr *runner.ToolError
	if !errors.As(err, &toolErr) || toolErr.ToolName != "unknown_tool" {
		t.Fatalf("Execute() error = %v, want ToolError for unknown_tool", err)
	}
}

func TestExecutePropagatesCancellationBeforeEveryStep(t *testing.T) {
	t.Parallel()

	t.Run("before run", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		m := &scriptedModel{responses: []model.Response{textResponse("late", model.Usage{})}}
		_, err := runner.Execute(ctx, m, nil, deps{}, model.Request{}, 1)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Execute() error = %v, want context.Canceled", err)
		}
		if len(m.requests) != 0 {
			t.Fatal("model was called after cancellation")
		}
	})

	t.Run("between tool executions", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		first := tool.MustNew(tool.Tool[deps]{
			Name:        "first",
			Description: "Cancels the run.",
			Schema:      json.RawMessage(`{"type":"object"}`),
			Exec: func(context.Context, deps, json.RawMessage) (string, error) {
				cancel()
				return "ok", nil
			},
		})
		secondExecutions := 0
		second := tool.MustNew(tool.Tool[deps]{
			Name:        "second",
			Description: "Must not run.",
			Schema:      json.RawMessage(`{"type":"object"}`),
			Exec: func(context.Context, deps, json.RawMessage) (string, error) {
				secondExecutions++
				return "", nil
			},
		})
		m := &scriptedModel{responses: []model.Response{
			toolResponse(
				model.ToolCall{ID: "c1", Name: "first", Args: json.RawMessage(`{}`)},
				model.ToolCall{ID: "c2", Name: "second", Args: json.RawMessage(`{}`)},
			),
		}}

		_, err := runner.Execute(ctx, m, []tool.Tool[deps]{first, second}, deps{},
			model.Request{ToolSpecs: specsFor(t, first, second)}, 3)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Execute() error = %v, want context.Canceled", err)
		}
		if secondExecutions != 0 {
			t.Fatal("second tool executed after cancellation")
		}
	})
}

func TestExecuteRejectsInvalidLimitsAndUnadvertisedTools(t *testing.T) {
	t.Parallel()

	if _, err := runner.Execute(context.Background(), &scriptedModel{}, nil, deps{}, model.Request{}, 0); err == nil {
		t.Fatal("Execute() error = nil, want maxIterations rejection")
	}

	echo := echoTool(t)
	if _, err := runner.Execute(context.Background(), &scriptedModel{}, []tool.Tool[deps]{echo}, deps{}, model.Request{}, 1); err == nil {
		t.Fatal("Execute() error = nil, want tools-without-specs rejection")
	}
}
