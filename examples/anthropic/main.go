// Command anthropic runs a minimal agent against the Anthropic Messages
// API: explicit configuration, including the MaxTokens bound the API
// requires.
//
// Set ANTHROPIC_API_KEY (and optionally ANTHROPIC_MODEL) to run it.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/anthropic"
)

func main() {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		fmt.Println("Set ANTHROPIC_API_KEY (and optionally ANTHROPIC_MODEL) to run this example.")
		return
	}
	modelName := os.Getenv("ANTHROPIC_MODEL")
	if modelName == "" {
		modelName = "claude-sonnet-4-5"
	}

	client, err := anthropic.New(anthropic.Config{
		APIKey:    apiKey,
		Model:     modelName,
		MaxTokens: 512,
	})
	if err != nil {
		fmt.Println("anthropic.New:", err)
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

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{},
		"What makes Go a good language for agent frameworks?")
	if err != nil {
		fmt.Println("Run:", err)
		return
	}
	fmt.Println(result.Output)
	fmt.Printf("usage: %d input, %d output tokens\n",
		result.Usage.InputTokens, result.Usage.OutputTokens)
}
