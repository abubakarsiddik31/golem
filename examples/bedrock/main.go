// Command bedrock runs a minimal agent against the AWS Bedrock Runtime
// Converse API, with requests signed using AWS Signature Version 4.
//
// Set AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_REGION (and
// optionally AWS_SESSION_TOKEN and BEDROCK_MODEL) to run it.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/bedrock"
)

func main() {
	accessKeyID := os.Getenv("AWS_ACCESS_KEY_ID")
	secretAccessKey := os.Getenv("AWS_SECRET_ACCESS_KEY")
	region := os.Getenv("AWS_REGION")
	if accessKeyID == "" || secretAccessKey == "" || region == "" {
		fmt.Println("Set AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, and AWS_REGION")
		fmt.Println("(optionally AWS_SESSION_TOKEN and BEDROCK_MODEL) to run this example.")
		return
	}
	modelID := os.Getenv("BEDROCK_MODEL")
	if modelID == "" {
		modelID = "us.anthropic.claude-sonnet-4-5-20250929-v1:0"
	}

	client, err := bedrock.New(bedrock.Config{
		Credentials: bedrock.Credentials{
			AccessKeyID:     accessKeyID,
			SecretAccessKey: secretAccessKey,
			SessionToken:    os.Getenv("AWS_SESSION_TOKEN"),
		},
		Region:    region,
		Model:     modelID,
		MaxTokens: 512,
	})
	if err != nil {
		fmt.Println("bedrock.New:", err)
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
