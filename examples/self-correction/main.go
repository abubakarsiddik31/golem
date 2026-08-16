// Command self-correction shows a tool that rejects correctable
// arguments: the die roll requires a positive count, and when the model
// gets it wrong the run feeds the rejection back so the model calls again
// within the configured budget.
//
// Set OPENAI_API_KEY (and optionally OPENAI_MODEL) to run it.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/openai"
	"github.com/abubakarsiddik31/golem/tool"
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

	roll := tool.MustNew(tool.Tool[struct{}]{
		Name:        "roll",
		Description: "Roll a six-sided die n times; n must be a positive integer.",
		Schema: json.RawMessage(`{
			"type": "object",
			"properties": {"n": {"type": "integer", "description": "How many times to roll."}},
			"required": ["n"]
		}`),
		Exec: func(ctx context.Context, deps struct{}, args json.RawMessage) (string, error) {
			var input struct {
				N int `json:"n"`
			}
			if err := json.Unmarshal(args, &input); err != nil {
				return "", err
			}
			if input.N <= 0 {
				// A rejection the model can fix, not a failure.
				return "", &model.ModelRetry{Err: fmt.Errorf("n must be positive, got %d", input.N)}
			}
			return fmt.Sprintf("rolled %d", input.N), nil
		},
	})

	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithTools[struct{}, string](roll),
		golem.WithToolRetries[struct{}, string](2),
	)
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{},
		"Roll the die zero times, then tell me what happened.")
	if err != nil {
		fmt.Println("Run:", err)
		return
	}

	fmt.Println(result.Output)
	for _, message := range result.Messages {
		if message.Role == model.RoleTool {
			fmt.Printf("tool %s returned %q\n", message.ToolName, message.Content)
		}
	}
}
