// Command cost prices a run's token usage: golem.WithPrice wires a
// model.Price so Result.Cost reports the cumulative spend and
// UsageLimit.Cost bounds it, failing the run at the usage stage when the
// priced total crosses the bound.
//
// The run is fully scripted — no network, no credentials. The same
// wiring prices a live provider when the scripted model is swapped for
// an adapter client and the rates come from that provider's price list.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/anthropic"
	"github.com/abubakarsiddik31/golem/testmodel"
	"github.com/abubakarsiddik31/golem/tool"
)

func main() {
	lookupCity := tool.MustNew(tool.Tool[struct{}]{
		Name:        "lookup_city",
		Description: "Look up a city's population.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
		Exec: func(ctx context.Context, deps struct{}, args json.RawMessage) (tool.Result, error) {
			return tool.Text("population 12,500,000"), nil
		},
	})

	// Two scripted turns: a tool-call turn, then the answered turn. Each
	// carries provider-style usage, which the price turns into dollars.
	client := testmodel.New().Respond(
		model.Response{
			Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
				{ID: "call-1", Name: "lookup_city", Args: json.RawMessage(`{"city":"Paris"}`)},
			}},
			Usage: model.Usage{InputTokens: 1_200, OutputTokens: 80, CacheReadTokens: 800},
		},
		model.Response{
			Message: model.Message{Role: model.RoleAssistant, Content: "Paris has about 12.5 million people."},
			Usage:   model.Usage{InputTokens: 1_500, OutputTokens: 120, CacheReadTokens: 1_000},
		},
	)

	// Anthropic reports cache reads and writes beside the input total, and
	// its Price encodes that combination; the rates are the application's
	// snapshot of the provider's price list.
	price := anthropic.Price{
		InputPerMTok:      3,
		OutputPerMTok:     15,
		CacheReadPerMTok:  0.30,
		CacheWritePerMTok: 3.75,
	}
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithTools[struct{}, string](lookupCity),
		golem.WithPrice[struct{}, string](price),
	)
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "how many people live in Paris?")
	if err != nil {
		fmt.Println("Run:", err)
		return
	}
	fmt.Printf("output: %s\n", result.Output)
	fmt.Printf("usage: %d in / %d out / %d cache-read tokens\n",
		result.Usage.InputTokens, result.Usage.OutputTokens, result.Usage.CacheReadTokens)
	fmt.Printf("cost: $%.6f across %d requests\n", result.Cost, result.Requests)

	// The same price becomes a spending bound: a limit the usage crosses
	// fails the run at the usage stage with the priced evidence preserved.
	bounded, err := golem.New[struct{}, string](testmodel.New().Respond(
		model.Response{
			Message: model.Message{Role: model.RoleAssistant, Content: "a long, expensive answer"},
			Usage:   model.Usage{InputTokens: 90_000, OutputTokens: 20_000},
		},
	),
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithPrice[struct{}, string](price),
		golem.WithUsageLimit[struct{}, string](golem.UsageLimit{Cost: 0.10}),
	)
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}
	if _, err := bounded.Run(context.Background(), golem.RunContext[struct{}]{}, "go"); err != nil {
		var runErr *golem.RunError
		if errors.As(err, &runErr) && runErr.Stage == golem.StageUsage && runErr.Partial != nil {
			fmt.Printf("run stopped at the %s stage: %v\n", runErr.Stage, err)
			fmt.Printf("priced evidence kept: $%.6f\n", runErr.Partial.Cost)
		} else {
			fmt.Println("Run:", err)
		}
	}
}
