// Command citations asks Anthropic to cite a PDF document and prints the
// source metadata on the final assistant message.
//
// Set ANTHROPIC_API_KEY and CITATIONS_DOCUMENT_URL (an https PDF URL the
// provider can fetch) to run it.
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
	documentURL := os.Getenv("CITATIONS_DOCUMENT_URL")
	if apiKey == "" || documentURL == "" {
		fmt.Println("Set ANTHROPIC_API_KEY and CITATIONS_DOCUMENT_URL (an https PDF URL) to run this example.")
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
		Citations: true,
	})
	if err != nil {
		fmt.Println("anthropic.New:", err)
		return
	}
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
	)
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{},
		"What does this document say about returns?",
		golem.WithPromptParts(model.DocumentURL(documentURL)))
	if err != nil {
		fmt.Println("Run:", err)
		return
	}
	fmt.Println(result.Output)
	var citations []model.Citation
	if len(result.Messages) > 0 {
		citations = result.Messages[len(result.Messages)-1].Citations
	}
	for i, citation := range citations {
		fmt.Printf("citation %d: type=%s title=%q url=%q chars=%d-%d page=%d text=%q\n",
			i+1, citation.Type, citation.Title, citation.URL,
			citation.StartCharIndex, citation.EndCharIndex, citation.PageNumber, citation.Text)
	}
}
