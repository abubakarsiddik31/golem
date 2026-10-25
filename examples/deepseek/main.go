// Command deepseek runs an agent against DeepSeek's OpenAI-compatible
// API, capturing the model's reasoning into thinking blocks — both the
// reasoning_content field the API returns and the <think> blocks some
// completion runtimes inline — while the answer itself stays content.
//
// Set DEEPSEEK_API_KEY (and optionally DEEPSEEK_MODEL) to run it.
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
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		fmt.Println("Set DEEPSEEK_API_KEY (and optionally DEEPSEEK_MODEL) to run this example.")
		return
	}
	modelName := os.Getenv("DEEPSEEK_MODEL")
	if modelName == "" {
		modelName = "deepseek-reasoner"
	}

	// DeepSeek speaks the OpenAI wire protocol: the preset base URL and
	// model are the only differences.
	client, err := openai.New(openai.Config{
		APIKey:  apiKey,
		BaseURL: "https://api.deepseek.com/v1",
		Model:   modelName,
	})
	if err != nil {
		fmt.Println("openai.New:", err)
		return
	}

	agent, err := golem.New[struct{}, string](
		client,
		golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) {
			return r.Message.Content, nil
		}),
	)
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}

	// The model's reasoning lands on the assistant message as thinking
	// blocks — captured from reasoning_content or a leading <think>
	// block, and never replayed to later requests.
	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{},
		"A bat and a ball cost one dollar ten in total. The bat costs one dollar more than the ball. How much is the ball?")
	if err != nil {
		fmt.Println("Run:", err)
		return
	}
	last := result.Messages[len(result.Messages)-1]
	for _, block := range last.Thinking {
		fmt.Println("--- reasoning ---")
		fmt.Println(block.Text)
	}
	fmt.Println("--- answer ---")
	fmt.Println(result.Output)
}
