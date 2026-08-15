package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/abubakarsiddik31/golem/internal/runner"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

type deps struct{ Tenant string }

// noRetries preserves the pre-retry behavior for tests that do not
// exercise ADR 0004.
var noRetries = runner.RetryConfig{MaxAttempts: 1}

// zeroBackoff keeps retry tests free of real waits.
func zeroBackoff(int) time.Duration { return 0 }

// scriptedModel returns queued errors first, in order, then queued
// responses, and records every request.
type scriptedModel struct {
	requests  []model.Request
	errs      []error
	responses []model.Response
}

func (m *scriptedModel) Generate(ctx context.Context, request model.Request) (model.Response, error) {
	m.requests = append(m.requests, request)
	if len(m.errs) > 0 {
		err := m.errs[0]
		m.errs = m.errs[1:]
		return model.Response{}, err
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
		model.Request{Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}}, 1, noRetries, 0)
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
		}, 5, noRetries, 0)
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
		}, 5, noRetries, 0)
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
		model.Request{ToolSpecs: specsFor(t, echo)}, 2, noRetries, 0)
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
		model.Request{ToolSpecs: specsFor(t, failing)}, 3, noRetries, 0)
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
		model.Request{ToolSpecs: specsFor(t, echo)}, 3, noRetries, 0)
	var toolErr *runner.ToolError
	if !errors.As(err, &toolErr) || toolErr.ToolName != "unknown_tool" {
		t.Fatalf("Execute() error = %v, want ToolError for unknown_tool", err)
	}
}

// validatingTool rejects non-positive n as correctable model content and
// accepts everything else.
func validatingTool(t *testing.T) tool.Tool[deps] {
	t.Helper()
	return tool.MustNew(tool.Tool[deps]{
		Name:        "roll",
		Description: "Roll a die; n must be positive.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"n":{"type":"integer"}}}`),
		Exec: func(_ context.Context, _ deps, args json.RawMessage) (string, error) {
			var input struct {
				N int `json:"n"`
			}
			if err := json.Unmarshal(args, &input); err != nil {
				return "", err
			}
			if input.N <= 0 {
				return "", &model.ModelRetry{Err: fmt.Errorf("n must be positive, got %d", input.N)}
			}
			return fmt.Sprintf("rolled %d", input.N), nil
		},
	})
}

// rejectingTool always returns the same ModelRetry and counts executions.
func rejectingTool(t *testing.T, reason error) (tool.Tool[deps], *int) {
	t.Helper()
	executions := 0
	rejecting := tool.MustNew(tool.Tool[deps]{
		Name:        "roll",
		Description: "Always rejects.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(context.Context, deps, json.RawMessage) (string, error) {
			executions++
			return "", &model.ModelRetry{Err: reason}
		},
	})
	return rejecting, &executions
}

func TestExecuteFeedsToolRejectionsBackToTheModel(t *testing.T) {
	t.Parallel()

	m := &scriptedModel{responses: []model.Response{
		toolResponse(model.ToolCall{ID: "call-1", Name: "roll", Args: json.RawMessage(`{"n":0}`)}),
		toolResponse(model.ToolCall{ID: "call-2", Name: "roll", Args: json.RawMessage(`{"n":4}`)}),
		textResponse("settled", model.Usage{InputTokens: 7, OutputTokens: 2}),
	}}
	roll := validatingTool(t)

	outcome, err := runner.Execute(context.Background(), m, []tool.Tool[deps]{roll}, deps{},
		model.Request{ToolSpecs: specsFor(t, roll)}, 5, noRetries, 1)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if outcome.Response.Message.Content != "settled" {
		t.Fatalf("final content = %q", outcome.Response.Message.Content)
	}
	if outcome.Usage != (model.Usage{InputTokens: 7, OutputTokens: 2}) {
		t.Fatalf("usage = %#v, want summed across turns", outcome.Usage)
	}

	// The second model turn must end with the rejection delivered as the
	// rejected call's tool result.
	if len(m.requests) != 3 {
		t.Fatalf("model turns = %d, want 3", len(m.requests))
	}
	second := m.requests[1].Messages
	rejection := second[len(second)-1]
	if rejection.Role != model.RoleTool || rejection.ToolCallID != "call-1" || rejection.ToolName != "roll" {
		t.Fatalf("rejection message = %#v", rejection)
	}
	if !strings.Contains(rejection.Content, "Your tool call was rejected") ||
		!strings.Contains(rejection.Content, "n must be positive, got 0") {
		t.Fatalf("rejection content = %q", rejection.Content)
	}

	// Evidence keeps the rejected call and both results, in order:
	// assistant-call, tool(rejection), assistant-call, tool(result),
	// assistant-final.
	wantRoles := []model.Role{model.RoleAssistant, model.RoleTool, model.RoleAssistant, model.RoleTool, model.RoleAssistant}
	if len(outcome.Messages) != len(wantRoles) {
		t.Fatalf("evidence = %#v", outcome.Messages)
	}
	for i, role := range wantRoles {
		if outcome.Messages[i].Role != role {
			t.Fatalf("evidence[%d].Role = %q, want %q", i, outcome.Messages[i].Role, role)
		}
	}
}

func TestExecuteWrapsCauseAfterExhaustedToolRetries(t *testing.T) {
	t.Parallel()

	reason := errors.New("n must be positive")
	rejecting, executions := rejectingTool(t, reason)
	m := &scriptedModel{responses: []model.Response{
		toolResponse(model.ToolCall{ID: "c1", Name: "roll", Args: json.RawMessage(`{}`)}),
		toolResponse(model.ToolCall{ID: "c2", Name: "roll", Args: json.RawMessage(`{}`)}),
		toolResponse(model.ToolCall{ID: "c3", Name: "roll", Args: json.RawMessage(`{}`)}),
	}}

	_, err := runner.Execute(context.Background(), m, []tool.Tool[deps]{rejecting}, deps{},
		model.Request{ToolSpecs: specsFor(t, rejecting)}, 5, noRetries, 2)
	var toolErr *runner.ToolError
	if !errors.As(err, &toolErr) || toolErr.ToolName != "roll" {
		t.Fatalf("Execute() error = %v, want ToolError for roll", err)
	}
	if !errors.Is(err, reason) {
		t.Fatalf("Execute() error = %v, want reason %v", err, reason)
	}
	var rejection *model.ModelRetry
	if !errors.As(err, &rejection) {
		t.Fatalf("Execute() error = %v, want ModelRetry in the chain", err)
	}
	if !strings.Contains(err.Error(), "after 3 attempts") {
		t.Fatalf("Execute() error = %q, want attempt count in message", err.Error())
	}
	if *executions != 3 || len(m.requests) != 3 {
		t.Fatalf("tool executions = %d, model turns = %d, want 3 and 3", *executions, len(m.requests))
	}
}

func TestExecuteAbortsOnToolRejectionWithoutBudget(t *testing.T) {
	t.Parallel()

	reason := errors.New("n must be positive")
	rejecting, executions := rejectingTool(t, reason)
	m := &scriptedModel{responses: []model.Response{
		toolResponse(model.ToolCall{ID: "c1", Name: "roll", Args: json.RawMessage(`{}`)}),
	}}

	_, err := runner.Execute(context.Background(), m, []tool.Tool[deps]{rejecting}, deps{},
		model.Request{ToolSpecs: specsFor(t, rejecting)}, 3, noRetries, 0)
	var toolErr *runner.ToolError
	if !errors.As(err, &toolErr) || toolErr.ToolName != "roll" {
		t.Fatalf("Execute() error = %v, want ToolError for roll", err)
	}
	if !errors.Is(err, reason) {
		t.Fatalf("Execute() error = %v, want reason %v", err, reason)
	}
	var rejection *model.ModelRetry
	if !errors.As(err, &rejection) {
		t.Fatalf("Execute() error = %v, want ModelRetry in the chain", err)
	}
	if strings.Contains(err.Error(), "after") {
		t.Fatalf("Execute() error = %q, default rejection must not carry an attempt count", err.Error())
	}
	if *executions != 1 || len(m.requests) != 1 {
		t.Fatalf("tool executions = %d, model turns = %d, want 1 and 1", *executions, len(m.requests))
	}
}

func TestExecuteStillAbortsPlainToolErrorsWithBudget(t *testing.T) {
	t.Parallel()

	cause := errors.New("database unavailable")
	failing := tool.MustNew(tool.Tool[deps]{
		Name:        "failing",
		Description: "Fails for real.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(context.Context, deps, json.RawMessage) (string, error) {
			return "", cause
		},
	})
	m := &scriptedModel{responses: []model.Response{
		toolResponse(model.ToolCall{ID: "c1", Name: "failing", Args: json.RawMessage(`{}`)}),
	}}

	_, err := runner.Execute(context.Background(), m, []tool.Tool[deps]{failing}, deps{},
		model.Request{ToolSpecs: specsFor(t, failing)}, 3, noRetries, 3)
	var toolErr *runner.ToolError
	if !errors.As(err, &toolErr) {
		t.Fatalf("Execute() error = %v, want ToolError", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("Execute() error = %v, want cause %v", err, cause)
	}
	if len(m.requests) != 1 {
		t.Fatalf("model turns = %d, want 1", len(m.requests))
	}
}

func TestExecuteRejectsNegativeToolRetries(t *testing.T) {
	t.Parallel()

	if _, err := runner.Execute(context.Background(), &scriptedModel{}, nil, deps{}, model.Request{}, 1,
		runner.RetryConfig{MaxAttempts: 1}, -1); err == nil {
		t.Fatal("Execute() error = nil, want negative toolRetries rejection")
	}
}

func TestExecutePropagatesCancellationBeforeEveryStep(t *testing.T) {
	t.Parallel()

	t.Run("before run", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		m := &scriptedModel{responses: []model.Response{textResponse("late", model.Usage{})}}
		_, err := runner.Execute(ctx, m, nil, deps{}, model.Request{}, 1, noRetries, 0)
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
			model.Request{ToolSpecs: specsFor(t, first, second)}, 3, noRetries, 0)
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

	if _, err := runner.Execute(context.Background(), &scriptedModel{}, nil, deps{}, model.Request{}, 0, noRetries, 0); err == nil {
		t.Fatal("Execute() error = nil, want maxIterations rejection")
	}

	echo := echoTool(t)
	if _, err := runner.Execute(context.Background(), &scriptedModel{}, []tool.Tool[deps]{echo}, deps{}, model.Request{}, 1, noRetries, 0); err == nil {
		t.Fatal("Execute() error = nil, want tools-without-specs rejection")
	}
}

// retryableFailure classifies itself retryable, like an adapter's 429/5xx
// APIError.
type retryableFailure struct{ message string }

func (e *retryableFailure) Error() string   { return e.message }
func (e *retryableFailure) Retryable() bool { return true }

// flakyModel fails its first failCalls calls with failure, then returns
// response, recording every call.
type flakyModel struct {
	failCalls int
	failure   error
	response  model.Response
	calls     int
}

func (m *flakyModel) Generate(ctx context.Context, request model.Request) (model.Response, error) {
	m.calls++
	if m.calls <= m.failCalls {
		return model.Response{}, m.failure
	}
	return m.response, nil
}

// cancelingModel cancels the run and then reports a retryable failure.
type cancelingModel struct {
	cancel  context.CancelFunc
	failure error
	calls   int
}

func (m *cancelingModel) Generate(ctx context.Context, request model.Request) (model.Response, error) {
	m.calls++
	m.cancel()
	return model.Response{}, m.failure
}

func TestExecuteRetriesRetryableModelFailures(t *testing.T) {
	t.Parallel()

	transient := &retryableFailure{message: "429 rate limited"}
	echo := echoTool(t)
	m := &scriptedModel{
		errs: []error{transient},
		responses: []model.Response{
			toolResponse(model.ToolCall{ID: "call-1", Name: "echo", Args: json.RawMessage(`{}`)}),
			textResponse("recovered", model.Usage{}),
		},
	}

	outcome, err := runner.Execute(context.Background(), m, []tool.Tool[deps]{echo}, deps{Tenant: "acme"},
		model.Request{
			Messages:  []model.Message{{Role: model.RoleUser, Content: "roll"}},
			ToolSpecs: specsFor(t, echo),
		}, 3, runner.RetryConfig{MaxAttempts: 2, Backoff: zeroBackoff}, 0)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if outcome.Response.Message.Content != "recovered" {
		t.Fatalf("final content = %q", outcome.Response.Message.Content)
	}
	// One failed attempt retried, then the recovered turn continued the
	// loop: user, assistant-call, tool, assistant-final.
	if len(m.requests) != 3 {
		t.Fatalf("model calls = %d, want 3 (failed attempt, tool turn, final turn)", len(m.requests))
	}
	if len(outcome.Messages) != 4 {
		t.Fatalf("evidence length = %d, want 4: %#v", len(outcome.Messages), outcome.Messages)
	}
}

func TestExecuteWrapsTerminalCauseAfterExhaustedRetries(t *testing.T) {
	t.Parallel()

	transient := &retryableFailure{message: "503 overloaded"}
	m := &flakyModel{failCalls: 100, failure: transient}
	var backoffAttempts []int

	_, err := runner.Execute(context.Background(), m, nil, deps{},
		model.Request{Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}}, 3,
		runner.RetryConfig{
			MaxAttempts: 3,
			Backoff:     func(attempt int) time.Duration { backoffAttempts = append(backoffAttempts, attempt); return 0 },
		}, 0)
	if !errors.Is(err, transient) {
		t.Fatalf("Execute() error = %v, want terminal cause %v", err, transient)
	}
	if !strings.Contains(err.Error(), "after 3 attempts") {
		t.Fatalf("Execute() error = %q, want attempt count in message", err.Error())
	}
	if m.calls != 3 {
		t.Fatalf("model attempts = %d, want 3", m.calls)
	}
	if !slices.Equal(backoffAttempts, []int{1, 2}) {
		t.Fatalf("backoff attempts = %v, want [1 2]", backoffAttempts)
	}
}

func TestExecuteReturnsNonRetryableFailuresUnchanged(t *testing.T) {
	t.Parallel()

	permanent := errors.New("unauthorized")
	m := &flakyModel{failCalls: 100, failure: permanent}

	_, err := runner.Execute(context.Background(), m, nil, deps{},
		model.Request{Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}}, 3,
		runner.RetryConfig{MaxAttempts: 3, Backoff: zeroBackoff}, 0)
	if err != permanent {
		t.Fatalf("Execute() error = %v, want the model error unchanged", err)
	}
	if m.calls != 1 {
		t.Fatalf("model attempts = %d, want 1", m.calls)
	}
}

func TestExecuteStopsRetryingWhenContextIsCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	m := &cancelingModel{cancel: cancel, failure: &retryableFailure{message: "429 rate limited"}}

	_, err := runner.Execute(ctx, m, nil, deps{}, model.Request{}, 3, runner.RetryConfig{
		MaxAttempts: 5,
		// The wait never elapses: the fake cancels ctx during Generate, so
		// the context-aware wait aborts immediately.
		Backoff: func(int) time.Duration { return time.Hour },
	}, 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute() error = %v, want context.Canceled", err)
	}
	if m.calls != 1 {
		t.Fatalf("model attempts = %d, want 1", m.calls)
	}
}

func TestExecuteRejectsInvalidRetryConfiguration(t *testing.T) {
	t.Parallel()

	if _, err := runner.Execute(context.Background(), &scriptedModel{}, nil, deps{}, model.Request{}, 1,
		runner.RetryConfig{}, 0); err == nil {
		t.Fatal("Execute() error = nil, want MaxAttempts rejection")
	}
}
