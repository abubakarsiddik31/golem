// Command token-counting prices a conversation before sending it: a
// growing history is bounded by token budget with golem.BudgetHistory,
// and a run enforces a per-request input ceiling with
// UsageLimit.PerRequestInputTokens — both over the tokens.Counter port.
//
// Set ANTHROPIC_API_KEY (and optionally ANTHROPIC_BASE, ANTHROPIC_MODEL)
// to run it. The count endpoint prices the same wire request the agent
// would send, so the numbers are the provider's own.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/anthropic"
	"github.com/abubakarsiddik31/golem/tokens"
)

// paragraph is a chunk of notes a conversation might accumulate.
var paragraphs = []string{
	"The quick brown fox jumps over the lazy dog while the farm sleeps.",
	"Token counting asks the provider to price a request before sending it, turning budgets into promises.",
	"Golem keeps run history as durable message evidence that a budget can trim without breaking tool pairing.",
	"Pre-send limits fail the run at the usage stage when an estimate crosses the bound, so nothing is billed.",
	"A history processor runs once per run, before validation and repair, and its error stops everything.",
}

func main() {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		fmt.Println("Set ANTHROPIC_API_KEY (and optionally ANTHROPIC_BASE, ANTHROPIC_MODEL) to run this example.")
		return
	}
	modelName := os.Getenv("ANTHROPIC_MODEL")
	if modelName == "" {
		modelName = "claude-sonnet-4-5"
	}

	counter, err := anthropic.NewCounter(anthropic.Config{
		APIKey:  apiKey,
		BaseURL: os.Getenv("ANTHROPIC_BASE"),
		Model:   modelName,
	})
	if err != nil {
		fmt.Println("anthropic.NewCounter:", err)
		return
	}
	client, err := anthropic.New(anthropic.Config{
		APIKey:    apiKey,
		BaseURL:   os.Getenv("ANTHROPIC_BASE"),
		Model:     modelName,
		MaxTokens: 256,
	})
	if err != nil {
		fmt.Println("anthropic.New:", err)
		return
	}

	agent, err := golem.New[any, string](client,
		golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) {
			return r.Message.Content, nil
		}),
		golem.WithTokenCounter[any, string](counter),
		golem.WithHistoryProcessor[any, string](golem.BudgetHistory(counter, 200)),
		golem.WithUsageLimit[any, string](golem.UsageLimit{PerRequestInputTokens: 250}),
	)
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}

	// Grow the conversation one paragraph at a time. BudgetHistory keeps
	// the newest turns the 200-token budget allows, so the request never
	// grows without bound.
	ctx := context.Background()
	var history []model.Message
	for i, paragraph := range paragraphs {
		prompt := fmt.Sprintf("Add this to your notes: %s", paragraph)
		result, err := agent.RunWithHistory(ctx, golem.RunContext[any]{}, history, prompt)
		if err != nil {
			var runErr *golem.RunError
			if errors.As(err, &runErr) && runErr.Stage == golem.StageUsage {
				fmt.Printf("turn %d: crossed the per-request limit, stopping: %v\n", i+1, runErr.Err)
				return
			}
			fmt.Println("Run:", err)
			return
		}
		history = append(history,
			model.Message{Role: model.RoleUser, Content: prompt},
			result.Messages[len(result.Messages)-1],
		)

		// The provider's own count of what the next request would carry.
		count, err := counter.CountTokens(ctx, tokens.CountInput{Messages: history})
		if err != nil {
			fmt.Println("CountTokens:", err)
			return
		}
		fmt.Printf("turn %d: history carries %d messages, next request prices ~%d input tokens\n",
			i+1, len(history), count)
	}
	fmt.Println("done — the budget kept every request inside the ceiling")
}
