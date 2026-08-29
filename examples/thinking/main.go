// Command thinking runs an agent with adaptive thinking enabled and
// shows where the model's reasoning lands in the run result.
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
		APIKey: apiKey,
		Model:  modelName,
		// Adaptive thinking lets the model decide when and how much to
		// think. Thinking is billed as output tokens, so the max-token
		// bound must leave room for it.
		MaxTokens: 2048,
		Thinking:  &anthropic.ThinkingConfig{Adaptive: true},
	})
	if err != nil {
		fmt.Println("anthropic.New:", err)
		return
	}
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithInstructions[struct{}, string]("Answer with the number only."),
	)
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{},
		"A bat and a ball cost $1.10 together. The bat costs $1.00 more than the ball. What does the ball cost?")
	if err != nil {
		fmt.Println("Run:", err)
		return
	}

	fmt.Println("answer:", result.Output)
	last := result.Messages[len(result.Messages)-1]
	if len(last.Thinking) == 0 {
		fmt.Println("(the model did not think this turn)")
		return
	}
	fmt.Println("reasoning:")
	for _, block := range last.Thinking {
		if block.Redacted != "" {
			fmt.Println("  [redacted reasoning]")
			continue
		}
		fmt.Printf("  %s\n", block.Text)
		if block.Signature != "" {
			fmt.Printf("  [signature present: %d chars]\n", len(block.Signature))
		}
	}
	// Keep the message history and the signatures ride along, so the
	// next turn verifies the reasoning automatically.
	fmt.Printf("usage: %d input, %d output tokens\n",
		result.Usage.InputTokens, result.Usage.OutputTokens)
}
