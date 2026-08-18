// Command fallback runs a prompt against a primary model with a backup
// model behind it: when the primary fails with a retryable error — rate
// limits, 5xx, transport faults — the run continues on the backup instead
// of failing. A request bound caps what one run may spend.
//
// Set OPENAI_API_KEY (and optionally OPENAI_MODEL and
// OPENAI_FALLBACK_MODEL) to run it.
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
		fmt.Println("Set OPENAI_API_KEY (and optionally OPENAI_MODEL and OPENAI_FALLBACK_MODEL) to run this example.")
		return
	}
	primaryName := envOrDefault("OPENAI_MODEL", "gpt-4o-mini")
	backupName := envOrDefault("OPENAI_FALLBACK_MODEL", "gpt-4.1-mini")

	primary, err := openai.New(openai.Config{APIKey: apiKey, Model: primaryName})
	if err != nil {
		fmt.Println("openai.New primary:", err)
		return
	}
	backup, err := openai.New(openai.Config{APIKey: apiKey, Model: backupName})
	if err != nil {
		fmt.Println("openai.New backup:", err)
		return
	}
	fallback, err := model.NewFallback(primary, backup)
	if err != nil {
		fmt.Println("model.NewFallback:", err)
		return
	}

	agent, err := golem.New[struct{}, string](fallback,
		golem.DecodeFunc[string](func(ctx context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithUsageLimit[struct{}, string](golem.UsageLimit{Requests: 4}),
	)
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{},
		"In one short sentence, why is a fallback model useful?")
	if err != nil {
		fmt.Println("Run:", err)
		return
	}
	fmt.Println(result.Output)
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
