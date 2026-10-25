package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem/internal/runner"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

func weatherTool(t *testing.T) tool.Tool[deps] {
	t.Helper()
	return tool.MustNew(tool.Tool[deps]{
		Name:        "weather",
		Description: "Report the weather forecast for a location.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"location":{"type":"string"}}}`),
		Exec: func(context.Context, deps, json.RawMessage) (tool.Result, error) {
			return tool.Text("sunny"), nil
		},
	})
}

func requestNames(request model.Request) []string {
	names := make([]string, len(request.ToolSpecs))
	for i, spec := range request.ToolSpecs {
		names[i] = spec.Name
	}
	return names
}

// executeWithSearch runs one loop with the given search policy and default
// iteration budget.
func executeWithSearch(t *testing.T, m *scriptedModel, tools []tool.Tool[deps], req model.Request, search *runner.SearchConfig, retries int) (runner.Outcome, error) {
	t.Helper()
	return runner.ExecuteWithToolConfig(context.Background(), m, tools, deps{Tenant: "acme"}, req, 5, noRetries,
		runner.ToolConfig{DefaultRetries: retries, Search: search}, "", nil, nil, runner.RunIdentity{})
}

func TestExecuteAdvertisesDeferredToolOnlyAfterSearch(t *testing.T) {
	t.Parallel()

	m := &scriptedModel{responses: []model.Response{
		toolResponse(model.ToolCall{ID: "s1", Name: runner.SearchToolName, Args: json.RawMessage(`{"query":"weather"}`)}),
		toolResponse(model.ToolCall{ID: "w1", Name: "weather", Args: json.RawMessage(`{"location":"Paris"}`)}),
		textResponse("sunny in Paris", model.Usage{}),
	}}
	eager := echoTool(t)
	deferred := weatherTool(t)
	tools := []tool.Tool[deps]{eager, deferred}
	req := model.Request{Messages: []model.Message{{Role: model.RoleUser, Content: "weather?"}}, ToolSpecs: specsFor(t, eager)}

	outcome, err := executeWithSearch(t, m, tools, req, &runner.SearchConfig{Deferred: []string{"weather"}}, 0)
	if err != nil {
		t.Fatalf("Execute = %v", err)
	}
	if len(m.requests) != 3 {
		t.Fatalf("model calls = %d, want 3", len(m.requests))
	}
	if got := requestNames(m.requests[0]); !reflect.DeepEqual(got, []string{"echo", runner.SearchToolName}) {
		t.Fatalf("first request specs = %v, want [echo %s]", got, runner.SearchToolName)
	}
	if got := requestNames(m.requests[1]); !reflect.DeepEqual(got, []string{"echo", runner.SearchToolName, "weather"}) {
		t.Fatalf("second request specs = %v, want [echo %s weather]", got, runner.SearchToolName)
	}
	if outcome.Response.Message.Content != "sunny in Paris" {
		t.Fatalf("final content = %q, want %q", outcome.Response.Message.Content, "sunny in Paris")
	}
	if outcome.ToolExecutions != 2 {
		t.Fatalf("tool executions = %d, want 2", outcome.ToolExecutions)
	}
}

func TestExecuteRejectsCallToUnrevealedDeferredTool(t *testing.T) {
	t.Parallel()

	m := &scriptedModel{responses: []model.Response{
		toolResponse(model.ToolCall{ID: "w1", Name: "weather", Args: json.RawMessage(`{"location":"Paris"}`)}),
	}}
	eager := echoTool(t)
	deferred := weatherTool(t)
	req := model.Request{Messages: []model.Message{{Role: model.RoleUser, Content: "weather?"}}, ToolSpecs: specsFor(t, eager)}

	_, err := executeWithSearch(t, m, []tool.Tool[deps]{eager, deferred}, req,
		&runner.SearchConfig{Deferred: []string{"weather"}}, 0)
	var toolErr *runner.ToolError
	if !errors.As(err, &toolErr) {
		t.Fatalf("Execute error = %v, want *runner.ToolError", err)
	}
	if toolErr.ToolName != "weather" {
		t.Fatalf("ToolError.ToolName = %q, want weather", toolErr.ToolName)
	}
	if !strings.Contains(err.Error(), runner.SearchToolName) {
		t.Fatalf("error %q does not name %s", err, runner.SearchToolName)
	}
	if len(m.requests) != 1 {
		t.Fatalf("model calls = %d, want 1", len(m.requests))
	}
}

func TestExecuteRejectsSameBatchReveal(t *testing.T) {
	t.Parallel()

	// A search in the same response cannot reveal a tool for a call the
	// model emitted before seeing the result.
	m := &scriptedModel{responses: []model.Response{
		toolResponse(
			model.ToolCall{ID: "s1", Name: runner.SearchToolName, Args: json.RawMessage(`{"query":"weather"}`)},
			model.ToolCall{ID: "w1", Name: "weather", Args: json.RawMessage(`{"location":"Paris"}`)},
		),
	}}
	eager := echoTool(t)
	deferred := weatherTool(t)
	req := model.Request{Messages: []model.Message{{Role: model.RoleUser, Content: "weather?"}}, ToolSpecs: specsFor(t, eager)}

	_, err := executeWithSearch(t, m, []tool.Tool[deps]{eager, deferred}, req,
		&runner.SearchConfig{Deferred: []string{"weather"}}, 0)
	var toolErr *runner.ToolError
	if !errors.As(err, &toolErr) || toolErr.ToolName != "weather" {
		t.Fatalf("Execute error = %v, want a weather *runner.ToolError", err)
	}
	if len(m.requests) != 1 {
		t.Fatalf("model calls = %d, want 1", len(m.requests))
	}
}

func TestExecuteKeepsRevealAcrossResumedRun(t *testing.T) {
	t.Parallel()

	eager := echoTool(t)
	deferred := weatherTool(t)
	tools := []tool.Tool[deps]{eager, deferred}
	search := &runner.SearchConfig{Deferred: []string{"weather"}}

	first := &scriptedModel{responses: []model.Response{
		toolResponse(model.ToolCall{ID: "s1", Name: runner.SearchToolName, Args: json.RawMessage(`{"query":"weather"}`)}),
		textResponse("ready", model.Usage{}),
	}}
	firstOutcome, err := executeWithSearch(t, first, tools,
		model.Request{Messages: []model.Message{{Role: model.RoleUser, Content: "weather?"}}, ToolSpecs: specsFor(t, eager)}, search, 0)
	if err != nil {
		t.Fatalf("first run = %v", err)
	}

	second := &scriptedModel{responses: []model.Response{
		toolResponse(model.ToolCall{ID: "w1", Name: "weather", Args: json.RawMessage(`{"location":"Paris"}`)}),
		textResponse("sunny", model.Usage{}),
	}}
	outcome, err := executeWithSearch(t, second, tools,
		model.Request{Messages: firstOutcome.Messages, ToolSpecs: specsFor(t, eager)}, search, 0)
	if err != nil {
		t.Fatalf("resumed run = %v", err)
	}
	if got := requestNames(second.requests[0]); !reflect.DeepEqual(got, []string{"echo", runner.SearchToolName, "weather"}) {
		t.Fatalf("resumed first request specs = %v, want [echo %s weather]", got, runner.SearchToolName)
	}
	if outcome.ToolExecutions != 1 {
		t.Fatalf("resumed tool executions = %d, want 1", outcome.ToolExecutions)
	}
}

func TestExecuteFeedsBackBadSearchQueryForCorrection(t *testing.T) {
	t.Parallel()

	m := &scriptedModel{responses: []model.Response{
		toolResponse(model.ToolCall{ID: "s1", Name: runner.SearchToolName, Args: json.RawMessage(`{"query":"  "}`)}),
		toolResponse(model.ToolCall{ID: "s2", Name: runner.SearchToolName, Args: json.RawMessage(`{"query":"weather"}`)}),
		textResponse("ready", model.Usage{}),
	}}
	eager := echoTool(t)
	deferred := weatherTool(t)
	req := model.Request{Messages: []model.Message{{Role: model.RoleUser, Content: "weather?"}}, ToolSpecs: specsFor(t, eager)}

	if _, err := executeWithSearch(t, m, []tool.Tool[deps]{eager, deferred}, req,
		&runner.SearchConfig{Deferred: []string{"weather"}}, 1); err != nil {
		t.Fatalf("Execute = %v", err)
	}

	var rejected bool
	for _, message := range m.requests[1].Messages {
		if message.Role == model.RoleTool && strings.Contains(message.Content, "non-empty query") {
			rejected = true
		}
	}
	if !rejected {
		t.Fatalf("second request did not carry the rejection feedback: %+v", m.requests[1].Messages)
	}
	if got := requestNames(m.requests[2]); !reflect.DeepEqual(got, []string{"echo", runner.SearchToolName, "weather"}) {
		t.Fatalf("third request specs = %v, want [echo %s weather]", got, runner.SearchToolName)
	}
}

func TestExecuteAllowsAllDeferredToolsWithoutBaseSpecs(t *testing.T) {
	t.Parallel()

	m := &scriptedModel{responses: []model.Response{
		toolResponse(model.ToolCall{ID: "s1", Name: runner.SearchToolName, Args: json.RawMessage(`{"query":"weather"}`)}),
		textResponse("ready", model.Usage{}),
	}}
	deferred := weatherTool(t)
	req := model.Request{Messages: []model.Message{{Role: model.RoleUser, Content: "weather?"}}}

	if _, err := executeWithSearch(t, m, []tool.Tool[deps]{deferred}, req,
		&runner.SearchConfig{Deferred: []string{"weather"}}, 0); err != nil {
		t.Fatalf("Execute = %v", err)
	}
	if got := requestNames(m.requests[0]); !reflect.DeepEqual(got, []string{runner.SearchToolName}) {
		t.Fatalf("first request specs = %v, want [%s]", got, runner.SearchToolName)
	}
}
