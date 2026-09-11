// Command run-cancellation shows a tool ending the run deliberately: a
// guard tool returns &tool.Canceled when the request crosses the budget
// it polices, the run stops at the canceled stage with the evidence —
// the executed calls before the stop, the closings after it — on
// RunError.Partial, and the transcript resumes through RunWithHistory.
// It runs against a scripted fake model — no network, no credentials,
// fully deterministic.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
	"github.com/abubakarsiddik31/golem/tool"
)

func main() {
	// The budget the guard polices lives in the dependency value, like any
	// application state a tool reads.
	type budget struct{ RemainingCents int }

	var deps budget

	charge := tool.MustNew(tool.Tool[budget]{
		Name:        "charge_card",
		Description: "Charge the card in cents.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"cents":{"type":"number"}},"required":["cents"]}`),
		Exec: func(ctx context.Context, deps budget, args json.RawMessage) (tool.Result, error) {
			return tool.Text("charged"), nil
		},
	})
	guard := tool.MustNew(tool.Tool[budget]{
		Name:        "budget_guard",
		Description: "Check the spend against the budget before charging.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"cents":{"type":"number"}},"required":["cents"]}`),
		Exec: func(ctx context.Context, deps budget, args json.RawMessage) (tool.Result, error) {
			if deps.RemainingCents <= 0 {
				// The deliberate stop: nothing after this call runs.
				return tool.Result{}, &tool.Canceled{Reason: "budget exhausted; end the run"}
			}
			return tool.Text("within budget"), nil
		},
	})
	decoder := golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
		return response.Message.Content, nil
	})

	// First run: the model charges, then asks the guard, which vetoes the
	// next request. The model never gets another turn.
	deps = budget{RemainingCents: 0}
	scripted := testmodel.New().Respond(
		model.Response{
			Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
				{ID: "call-1", Name: "charge_card", Args: json.RawMessage(`{"cents":500}`)},
				{ID: "call-2", Name: "budget_guard", Args: json.RawMessage(`{"cents":900}`)},
			}},
			Usage: model.Usage{InputTokens: 14, OutputTokens: 5},
		},
	).Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "Planned within budget."}},
	)
	agent, err := golem.New[budget, string](scripted, decoder,
		golem.WithTools[budget, string](charge, guard))
	if err != nil {
		log.Fatal("golem.New: ", err)
	}

	_, err = agent.Run(context.Background(), golem.RunContext[budget]{Deps: deps}, "buy supplies")
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageCanceled {
		log.Fatal("Run: expected a canceled-stage RunError, got: ", err)
	}
	var stopped *tool.Canceled
	if !errors.As(err, &stopped) {
		log.Fatal("Run: expected the tool.Canceled sentinel, got: ", err)
	}
	partial := runErr.Partial
	fmt.Printf("run canceled: %s\n", stopped.Reason)
	fmt.Printf("evidence: %d messages, %d requests, %d tool calls — transcript still resumable\n",
		len(partial.Messages), partial.Requests, partial.ToolCalls)

	// The canceled transcript is already provider-valid: resume without
	// repair and the run continues inside the budget.
	result, err := agent.RunWithHistory(context.Background(), golem.RunContext[budget]{Deps: budget{RemainingCents: 2000}},
		partial.Messages, "the budget was raised; plan again")
	if err != nil {
		log.Fatal("RunWithHistory: ", err)
	}
	fmt.Printf("resumed: %s\n", result.Output)
}
