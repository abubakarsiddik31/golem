// Command streaming prints a response as it arrives: RunStream forwards
// every model fragment across tool turns and correction rounds while
// producing the same Result as Run.
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
		}))
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}

	result, err := agent.RunStream(context.Background(), golem.RunContext[struct{}]{},
		"Count from one to five as digits, like 12345.",
		func(d model.Delta) error {
			fmt.Print(d.Content)
			return nil
		})
	if err != nil {
		fmt.Println("\nRunStream:", err)
		return
	}
	fmt.Printf("\nfinished: %d messages, %d output tokens\n",
		len(result.Messages), result.Usage.OutputTokens)
}
