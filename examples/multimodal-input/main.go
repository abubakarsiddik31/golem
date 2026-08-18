// Command multimodal-input attaches an inline image to a run's prompt and
// asks the model to describe it. The image is a 1x1 red pixel embedded in
// the program, so the only network use is the model call itself.
//
// Set OPENAI_API_KEY (and optionally OPENAI_MODEL) to run it.
package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/openai"
)

// redPixelPNG is a 1x1 red PNG; inline data keeps the example independent
// of any image host.
const redPixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC"

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
		golem.DecodeFunc[string](func(ctx context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}))
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}

	pixels, err := base64.StdEncoding.DecodeString(redPixelPNG)
	if err != nil {
		fmt.Println("decode embedded image:", err)
		return
	}
	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{},
		"Describe this image in one short sentence.",
		golem.WithPromptImageData("image/png", pixels))
	if err != nil {
		fmt.Println("Run:", err)
		return
	}
	fmt.Println(result.Output)
}
