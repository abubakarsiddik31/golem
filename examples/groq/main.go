// Command groq runs an agent against Groq's fast OpenAI-compatible
// inference through the providers/groq preset: base URL and key
// handling come from the preset, the wire protocol and reasoning
// extraction come from providers/openai.
//
// Set GROQ_API_KEY (and optionally GROQ_MODEL) to run it.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/groq"
	"github.com/abubakarsiddik31/golem/providers/openai"
)

func main() {
	apiKey := os.Getenv("GROQ_API_KEY")
	if apiKey == "" {
		fmt.Println("Set GROQ_API_KEY (and optionally GROQ_MODEL) to run this example.")
		return
	}
	modelName := os.Getenv("GROQ_MODEL")
	if modelName == "" {
		modelName = groq.ModelLlama3370B
	}

	client, err := groq.New(openai.Config{APIKey: apiKey, Model: modelName})
	if err != nil {
		fmt.Println("groq.New:", err)
		return
	}

	agent, err := golem.New[struct{}, string](
		client,
		golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) {
			return r.Message.Content, nil
		}),
		golem.WithInstructionsFunc[struct{}, string](func(context.Context, golem.RunContext[struct{}]) string {
			return "Answer in one sentence."
		}),
	)
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{},
		"Why does inference speed matter for agents?")
	if err != nil {
		fmt.Println("Run:", err)
		return
	}
	fmt.Println(result.Output)
}
