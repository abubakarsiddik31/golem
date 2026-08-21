// Command delegation runs a planner agent whose only tool is another
// agent: the model delegates a claim to the fact-checking specialist,
// Golem runs it as a sub-agent with the shared dependency value, and the
// planner answers from the rendered result.
//
// Set OPENAI_API_KEY (and optionally OPENAI_MODEL) to run it.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/openai"
)

// config is the shared dependency value: the specialist consults it too.
type config struct {
	AnswerStyle string
}

func decodeContent(_ context.Context, r model.Response) (string, error) {
	return r.Message.Content, nil
}

func main() {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		fmt.Println("Set OPENAI_API_KEY (and optionally OPENAI_MODEL) to run this example.")
		return
	}
	modelName := os.Getenv("OPENAI_MODEL")
	if modelName == "" {
		modelName = "gpt-4o-mini"
	}

	client, err := openai.New(openai.Config{APIKey: apiKey, Model: modelName})
	if err != nil {
		fmt.Println("openai.New:", err)
		return
	}

	// The specialist answers one claim at a time, in the configured style.
	specialist, err := golem.New[config, string](
		client,
		golem.DecodeFunc[string](decodeContent),
		golem.WithInstructionsFunc[config, string](func(_ context.Context, runCtx golem.RunContext[config]) string {
			return "You are a fact checker. Answer with a one-sentence verdict, style: " + runCtx.Deps.AnswerStyle + "."
		}),
	)
	if err != nil {
		fmt.Println("specialist New:", err)
		return
	}
	factCheck, err := specialist.AsTool("fact_checker",
		"Checks one factual claim and answers with the verdict.")
	if err != nil {
		fmt.Println("AsTool:", err)
		return
	}

	// The planner must consult the specialist before it answers.
	planner, err := golem.New[config, string](
		client,
		golem.DecodeFunc[string](decodeContent),
		golem.WithInstructions[config, string]("You plan before answering: delegate every factual claim to the fact_checker tool, then answer from its verdicts."),
		golem.WithTools[config, string](factCheck),
	)
	if err != nil {
		fmt.Println("planner New:", err)
		return
	}

	ctx := context.Background()
	prompt := "Is the Eiffel Tower taller than the towers of the Golden Gate Bridge?"
	fmt.Println("Question:", prompt)
	result, err := planner.Run(ctx, golem.RunContext[config]{Deps: config{AnswerStyle: "terse"}}, prompt)
	if err != nil {
		fmt.Println("Run:", err)
		return
	}
	fmt.Println("Answer:", result.Output)
	fmt.Printf("Evidence: %d messages, %d tokens\n", len(result.Messages), result.Usage.InputTokens+result.Usage.OutputTokens)
}
