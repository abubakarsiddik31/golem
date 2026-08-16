// Command conversation chains runs into a multi-turn chat: each result's
// messages become the next run's history, and instructions are re-applied
// per run.
//
// Set OPENAI_API_KEY (and optionally OPENAI_MODEL) to run it.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/openai"
)

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
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithInstructions[struct{}, string]("Answer in one short sentence."),
	)
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}

	ctx := context.Background()
	runCtx := golem.RunContext[struct{}]{}
	var history []model.Message

	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("Ask anything (empty line to quit):")
	for scanner.Scan() {
		prompt := strings.TrimSpace(scanner.Text())
		if prompt == "" {
			break
		}

		var result golem.Result[string]
		var err error
		if history == nil {
			result, err = agent.Run(ctx, runCtx, prompt)
		} else {
			result, err = agent.RunWithHistory(ctx, runCtx, history, prompt)
		}
		if err != nil {
			fmt.Println("run:", err)
			continue
		}
		history = result.Messages

		fmt.Println("agent:", result.Output)
		fmt.Printf("(%d messages in conversation)\n", len(history))
	}
}
