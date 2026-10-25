// Command orchestration composes three agents in plain Go — no graph
// engine, no framework: two specialists run concurrently (fan-out),
// and their typed outputs feed a synthesizer's prompt (pipeline). It
// runs against scripted fake models — no network, no credentials,
// fully deterministic.
//
// The patterns here are the guide's: the application owns the
// goroutines, the error aggregation, and the context; Golem owns the
// runs and guarantees each result's conversation is immutable.
package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
)

func decodeContent(_ context.Context, r model.Response) (string, error) {
	return r.Message.Content, nil
}

// specialist builds one agent over its own scripted model, so the
// concurrent runs never share state.
func specialist(instructions, reply string) *golem.Agent[struct{}, string] {
	m := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: reply}},
	)
	agent, err := golem.New[struct{}, string](m,
		golem.DecodeFunc[string](decodeContent),
		golem.WithInstructionsFunc[struct{}, string](func(context.Context, golem.RunContext[struct{}]) string {
			return instructions
		}),
	)
	if err != nil {
		panic(err) // the configuration is static; nothing else can fail
	}
	return agent
}

// fanOut runs every agent concurrently on the same prompt. Error
// delivery: one slot per agent, no shared state; the first context
// cancellation stops new work and surfaces on that agent's Run.
func fanOut(ctx context.Context, prompt string, agents ...*golem.Agent[struct{}, string]) ([]string, error) {
	answers := make([]string, len(agents))
	errs := make([]error, len(agents))
	var wg sync.WaitGroup
	for i, agent := range agents {
		wg.Add(1)
		go func(i int, agent *golem.Agent[struct{}, string]) {
			defer wg.Done()
			result, err := agent.Run(ctx, golem.RunContext[struct{}]{}, prompt)
			if err != nil {
				errs[i] = err
				return
			}
			answers[i] = result.Output
		}(i, agent)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return answers, nil
}

func main() {
	summarizer := specialist("Summarize the input in one sentence.",
		"The team shipped the release two days early.")
	critic := specialist("Name the single biggest risk in the input, in one sentence.",
		"The early release skipped the full regression pass.")

	// Fan-out: both specialists run at once, each with its own run
	// identity, on the caller's context.
	answers, err := fanOut(context.Background(), "Review the release decision.",
		summarizer, critic)
	if err != nil {
		fmt.Println("fan-out:", err)
		return
	}

	// Pipeline: the typed outputs become the next agent's input.
	synthesizer := specialist("Combine the summary and the risk into one verdict.",
		"Ship it: delivered early, but schedule the regression pass before announcing.")
	result, err := synthesizer.Run(context.Background(), golem.RunContext[struct{}]{},
		fmt.Sprintf("Summary: %s\nRisk: %s", answers[0], answers[1]))
	if err != nil {
		fmt.Println("pipeline:", err)
		return
	}
	fmt.Println("summary:", answers[0])
	fmt.Println("risk:  ", answers[1])
	fmt.Println("verdict:", result.Output)
}
