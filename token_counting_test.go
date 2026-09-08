package golem_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
	"github.com/abubakarsiddik31/golem/tokens"
)

// countingCounter counts one message per token and records every input
// it was asked to price.
type countingCounter struct {
	inputs []tokens.CountInput
	fail   error
}

func (c *countingCounter) CountTokens(_ context.Context, input tokens.CountInput) (int, error) {
	if c.fail != nil {
		return 0, c.fail
	}
	c.inputs = append(c.inputs, input)
	count := 0
	for range input.Messages {
		count++
	}
	return count, nil
}

func TestBudgetHistoryKeepsHistoryUnderBudget(t *testing.T) {
	t.Parallel()

	counter := &countingCounter{}
	processor := golem.BudgetHistory(counter, 3)
	history := []model.Message{
		{Role: model.RoleUser, Content: "one"},
		{Role: model.RoleAssistant, Content: "two"},
		{Role: model.RoleUser, Content: "three"},
		{Role: model.RoleAssistant, Content: "four"},
	}
	kept, err := processor(context.Background(), history)
	if err != nil {
		t.Fatalf("BudgetHistory() error = %v", err)
	}
	if len(kept) != 3 {
		t.Fatalf("kept %d messages, want 3 (four messages over a budget of 3)", len(kept))
	}
	if kept[0].Content != "two" {
		t.Fatalf("kept[0] = %q, want the newest suffix", kept[0].Content)
	}
}

func TestBudgetHistoryAdvancesPastUnopenableHead(t *testing.T) {
	t.Parallel()

	counter := &countingCounter{}
	processor := golem.BudgetHistory(counter, 2)
	history := []model.Message{
		{Role: model.RoleUser, Content: "one"},
		{Role: model.RoleAssistant, Content: "two"},
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "c1", Name: "t"}}},
		{Role: model.RoleTool, ToolCallID: "c1", Content: "result"},
		{Role: model.RoleUser, Content: "three"},
		{Role: model.RoleAssistant, Content: "four"},
	}
	kept, err := processor(context.Background(), history)
	if err != nil {
		t.Fatalf("BudgetHistory() error = %v", err)
	}
	if len(kept) != 2 {
		t.Fatalf("kept %d messages, want 2", len(kept))
	}
	if kept[0].Content != "three" {
		t.Fatalf("kept[0] = %q, want the boundary rule to skip the tool pair", kept[0].Content)
	}
}

func TestBudgetHistoryErrorsWithoutCounterOrBudget(t *testing.T) {
	t.Parallel()

	if _, err := golem.BudgetHistory(nil, 10)(context.Background(), []model.Message{{Role: model.RoleUser}}); err == nil {
		t.Fatal("BudgetHistory(nil, ...) error = nil, want failure")
	}
	if _, err := golem.BudgetHistory(&countingCounter{}, 0)(context.Background(), []model.Message{{Role: model.RoleUser}}); err == nil {
		t.Fatal("BudgetHistory(counter, 0) error = nil, want failure")
	}
}

func TestBudgetHistoryFailsWhenSingleTurnExceedsBudget(t *testing.T) {
	t.Parallel()

	fat := testmodel.CountFunc(func(_ context.Context, input tokens.CountInput) (int, error) {
		return 10 * len(input.Messages), nil
	})
	_, err := golem.BudgetHistory(fat, 5)(context.Background(), []model.Message{
		{Role: model.RoleUser, Content: "one"},
		{Role: model.RoleAssistant, Content: "two"},
	})
	if err == nil || !strings.Contains(err.Error(), "over budget") {
		t.Fatalf("error = %v, want single-turn-over-budget failure", err)
	}
}

func TestBudgetHistoryPropagatesCounterFailure(t *testing.T) {
	t.Parallel()

	boom := errors.New("count endpoint down")
	processor := golem.BudgetHistory(&countingCounter{fail: boom}, 10)
	_, err := processor(context.Background(), []model.Message{{Role: model.RoleUser, Content: "one"}})
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want the counter error", err)
	}
}

func TestPerRequestInputTokensRequiresCounter(t *testing.T) {
	t.Parallel()

	_, err := golem.New[playerDeps, string](&queuedModel{}, decoderOf(),
		golem.WithUsageLimit[playerDeps, string](golem.UsageLimit{PerRequestInputTokens: 100}))
	if err == nil || !strings.Contains(err.Error(), "WithTokenCounter") {
		t.Fatalf("New() error = %v, want the counter requirement", err)
	}
}

func TestPreSendLimitStopsRunBeforeTheRequest(t *testing.T) {
	t.Parallel()

	// Two messages over a one-token budget: the estimate crosses before
	// any model call, so the scripted model must never generate.
	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, Content: "should never happen"}},
	}}
	agent, err := golem.New[playerDeps, string](client, decoderOf(),
		golem.WithTokenCounter[playerDeps, string](testmodel.CountFunc(
			func(_ context.Context, input tokens.CountInput) (int, error) {
				return len(input.Messages), nil
			})),
		golem.WithUsageLimit[playerDeps, string](golem.UsageLimit{PerRequestInputTokens: 1}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.RunWithHistory(context.Background(), golem.RunContext[playerDeps]{},
		[]model.Message{
			{Role: model.RoleUser, Content: "one"},
			{Role: model.RoleAssistant, Content: "two"},
		}, "go")
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageUsage {
		t.Fatalf("error = %v, want a usage-stage RunError", err)
	}
	var limitErr *golem.UsageLimitError
	if !errors.As(err, &limitErr) || limitErr.Kind != "per-request input token" || limitErr.Limit != 1 || limitErr.Actual != 3 {
		t.Fatalf("error = %v, want the per-request input token crossing (two history messages plus the prompt)", err)
	}
	if len(client.requests) != 0 {
		t.Fatalf("model called %d times, want zero — the request must not be sent", len(client.requests))
	}
	if runErr.Partial == nil || runErr.Partial.Requests != 0 {
		t.Fatalf("Partial = %+v, want the caller's history preserved with zero requests", runErr.Partial)
	}
}

func TestPreSendLimitPassesWhenUnderBudget(t *testing.T) {
	t.Parallel()

	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, Content: "fine"}},
	}}
	agent, err := golem.New[playerDeps, string](client, decoderOf(),
		golem.WithTokenCounter[playerDeps, string](testmodel.CountFunc(
			func(_ context.Context, input tokens.CountInput) (int, error) {
				return len(input.Messages), nil
			})),
		golem.WithUsageLimit[playerDeps, string](golem.UsageLimit{PerRequestInputTokens: 10}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[playerDeps]{}, "go")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "fine" {
		t.Fatalf("Output = %q, want the scripted answer", result.Output)
	}
}

func TestPreSendCounterFailureFailsRunAtModelStage(t *testing.T) {
	t.Parallel()

	boom := errors.New("count endpoint down")
	agent, err := golem.New[playerDeps, string](&queuedModel{}, decoderOf(),
		golem.WithTokenCounter[playerDeps, string](testmodel.CountFunc(
			func(_ context.Context, _ tokens.CountInput) (int, error) {
				return 0, boom
			})),
		golem.WithUsageLimit[playerDeps, string](golem.UsageLimit{PerRequestInputTokens: 10}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.Run(context.Background(), golem.RunContext[playerDeps]{}, "go")
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageModel {
		t.Fatalf("error = %v, want a model-stage RunError", err)
	}
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want the counter error matchable", err)
	}
}

func TestPreSendLimitRunsOnStreamedTurns(t *testing.T) {
	t.Parallel()

	client := &streamQueuedModel{queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, Content: "nope"}},
	}}}
	agent, err := golem.New[playerDeps, string](client, decoderOf(),
		golem.WithTokenCounter[playerDeps, string](testmodel.CountFunc(
			func(_ context.Context, input tokens.CountInput) (int, error) {
				return len(input.Messages), nil
			})),
		golem.WithUsageLimit[playerDeps, string](golem.UsageLimit{PerRequestInputTokens: 0}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	// Zero bound disables the dimension entirely: the run proceeds.
	if _, err := agent.RunStream(context.Background(), golem.RunContext[playerDeps]{}, "go",
		func(model.Delta) error { return nil }); err != nil {
		t.Fatalf("RunStream() error = %v, want a zero bound to be a no-op", err)
	}
}
