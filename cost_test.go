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

// perTokenPrice prices every token — input and output — at one flat rate,
// which keeps the test arithmetic exactly representable.
type perTokenPrice float64

var _ model.Price = perTokenPrice(0)

func (p perTokenPrice) Cost(usage model.Usage) float64 {
	return float64(usage.InputTokens+usage.OutputTokens) * float64(p)
}

func TestResultCostPricesCumulativeUsage(t *testing.T) {
	t.Parallel()

	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, Content: "almost"}, Usage: model.Usage{InputTokens: 3, OutputTokens: 2}},
		{Message: model.Message{Role: model.RoleAssistant, Content: "exact"}, Usage: model.Usage{InputTokens: 5, OutputTokens: 1}},
	}}
	agent, err := golem.New[struct{}, string](client, strictDecoder("exact"),
		golem.WithOutputRetries[struct{}, string](1),
		golem.WithPrice[struct{}, string](perTokenPrice(0.5)))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "answer exactly")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// The correction round's usage joins the first turn's: 8 in + 3 out,
	// priced at half a dollar per token.
	if result.Usage.InputTokens != 8 || result.Usage.OutputTokens != 3 {
		t.Fatalf("Usage = %+v, want both turns summed", result.Usage)
	}
	if result.Cost != 5.5 {
		t.Fatalf("Cost = %v, want 5.5 (eleven tokens at 0.5)", result.Cost)
	}
}

func TestCostLimitStopsRunAtUsageStage(t *testing.T) {
	t.Parallel()

	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, Content: "expensive"}, Usage: model.Usage{InputTokens: 12, OutputTokens: 1}},
	}}
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithPrice[struct{}, string](perTokenPrice(1)),
		golem.WithUsageLimit[struct{}, string](golem.UsageLimit{Cost: 5.5}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageUsage {
		t.Fatalf("error = %v, want a usage-stage RunError", err)
	}
	var limitErr *golem.UsageLimitError
	if !errors.As(err, &limitErr) || limitErr.Kind != "cost" || limitErr.Limit != 5.5 || limitErr.Actual != 13 {
		t.Fatalf("error = %v, want the cost crossing (13 dollars of tokens over a 5.5 limit)", err)
	}
	if !strings.Contains(err.Error(), "cost limit of 5.5 (used 13)") {
		t.Fatalf("error text = %q, want the float bounds rendered", err.Error())
	}
	if runErr.Partial == nil || runErr.Partial.Cost != 13 || runErr.Partial.Requests != 1 {
		t.Fatalf("Partial = %+v, want the crossing turn's cost and request preserved", runErr.Partial)
	}
}

func TestRunWithoutPriceReportsZeroCost(t *testing.T) {
	t.Parallel()

	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, Content: "free"}, Usage: model.Usage{InputTokens: 9, OutputTokens: 9}},
	}}
	agent, err := golem.New[struct{}, string](client, decoderOf())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Cost != 0 {
		t.Fatalf("Cost = %v, want zero without WithPrice", result.Cost)
	}
}

func TestPendingRunCarriesCost(t *testing.T) {
	t.Parallel()

	deferredDelete := tool.MustNew(tool.Tool[gateDeps]{
		Name:        "delete_file",
		Description: "Delete a file.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
		Exec: func(ctx context.Context, deps gateDeps, args json.RawMessage) (tool.Result, error) {
			return tool.Result{}, &tool.Deferred{Kind: tool.DeferApproval, Reason: "deletes need sign-off"}
		},
	})
	client := &queuedModel{responses: []model.Response{pauseResponse("delete_file")}}
	agent, err := golem.New[gateDeps, string](client,
		golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) {
			return r.Message.Content, nil
		}),
		golem.WithTools[gateDeps, string](deferredDelete),
		golem.WithPrice[gateDeps, string](perTokenPrice(0.25)))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[gateDeps]{}, "delete notes.txt")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Pending == nil {
		t.Fatal("Result.Pending is nil, want the deferred call")
	}
	// pauseResponse reports 7 in + 3 out.
	if result.Cost != 2.5 {
		t.Fatalf("Cost = %v, want 2.5 (ten tokens at 0.25)", result.Cost)
	}
}

func TestStreamedRunCarriesCost(t *testing.T) {
	t.Parallel()

	client := &streamQueuedModel{queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, Content: "streamed"}, Usage: model.Usage{InputTokens: 4, OutputTokens: 2}},
	}}}
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithPrice[struct{}, string](perTokenPrice(0.5)))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.RunStream(context.Background(), golem.RunContext[struct{}]{}, "go",
		func(model.Delta) error { return nil })
	if err != nil {
		t.Fatalf("RunStream() error = %v", err)
	}
	if result.Cost != 3 {
		t.Fatalf("Cost = %v, want 3 (six tokens at 0.5)", result.Cost)
	}
}

func TestCostLimitRequiresPrice(t *testing.T) {
	t.Parallel()

	_, err := golem.New[struct{}, string](&queuedModel{}, decoderOf(),
		golem.WithUsageLimit[struct{}, string](golem.UsageLimit{Cost: 1}))
	if err == nil || !strings.Contains(err.Error(), "WithPrice") {
		t.Fatalf("New() error = %v, want the price requirement", err)
	}
}

func TestNegativeCostLimitFailsNew(t *testing.T) {
	t.Parallel()

	_, err := golem.New[struct{}, string](&queuedModel{}, decoderOf(),
		golem.WithPrice[struct{}, string](perTokenPrice(1)),
		golem.WithUsageLimit[struct{}, string](golem.UsageLimit{Cost: -1}))
	if err == nil || !strings.Contains(err.Error(), "must not be negative") {
		t.Fatalf("New() error = %v, want the negative-bound rejection", err)
	}
}
