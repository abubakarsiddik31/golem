// Command azure runs a minimal agent against Azure OpenAI: the wire
// format matches OpenAI chat completions, but requests target a named
// deployment with an explicit API version and the api-key header.
//
// Set AZURE_OPENAI_API_KEY, AZURE_OPENAI_ENDPOINT,
// AZURE_OPENAI_DEPLOYMENT, and AZURE_OPENAI_API_VERSION to run it.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/azure"
)

func main() {
	apiKey := os.Getenv("AZURE_OPENAI_API_KEY")
	endpoint := os.Getenv("AZURE_OPENAI_ENDPOINT")
	deployment := os.Getenv("AZURE_OPENAI_DEPLOYMENT")
	apiVersion := os.Getenv("AZURE_OPENAI_API_VERSION")
	if apiKey == "" || endpoint == "" || deployment == "" || apiVersion == "" {
		fmt.Println("Set AZURE_OPENAI_API_KEY, AZURE_OPENAI_ENDPOINT,")
		fmt.Println("AZURE_OPENAI_DEPLOYMENT, and AZURE_OPENAI_API_VERSION to run this example.")
		return
	}

	client, err := azure.New(azure.Config{
		APIKey:     apiKey,
		Endpoint:   endpoint,
		Deployment: deployment,
		APIVersion: apiVersion,
	})
	if err != nil {
		fmt.Println("azure.New:", err)
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
