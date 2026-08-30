// Command local-models runs an agent against a local OpenAI-compatible
// runtime — Ollama or LM Studio — through the standard openai adapter,
// with one typed tool. It is the same shape as any provider example:
// only the base URL is local.
//
// Set GOLEM_LOCAL_BASE_URL (and optionally LOCAL_MODEL) to run it.
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

// workshop is the dependency value every tool in the run receives.
type workshop struct {
	Parts map[string]string
}

func main() {
	baseURL := os.Getenv("GOLEM_LOCAL_BASE_URL")
	if baseURL == "" {
		fmt.Println("Set GOLEM_LOCAL_BASE_URL to a local OpenAI-compatible server to run this example.")
		fmt.Println()
		fmt.Println("Ollama:    ollama pull qwen3 && GOLEM_LOCAL_BASE_URL=http://localhost:11434/v1 go run ./examples/local-models")
		fmt.Println("LM Studio: lms server start && GOLEM_LOCAL_BASE_URL=http://localhost:1234/v1 go run ./examples/local-models")
		fmt.Println()
		fmt.Println("Optionally set LOCAL_MODEL to the model to load (default qwen3).")
		return
	}
	modelName := os.Getenv("LOCAL_MODEL")
	if modelName == "" {
		modelName = "qwen3"
	}

	// Local runtimes ignore the Authorization header, but the adapter
	// requires a non-empty key; any placeholder works.
	client, err := openai.New(openai.Config{
		APIKey:  "golem-local",
		BaseURL: baseURL,
		Model:   modelName,
	})
	if err != nil {
		fmt.Println("openai.New:", err)
		return
	}

	lookupPart := tool.MustNew(tool.Tool[workshop]{
		Name:        "lookup_part",
		Description: "Look up one robot part by name and return its stock note.",
		Schema: json.RawMessage(`{
			"type": "object",
			"properties": {"part": {"type": "string", "description": "The part name, e.g. memory or servo."}},
			"required": ["part"]
		}`),
		Exec: func(ctx context.Context, deps workshop, args json.RawMessage) (string, error) {
			var input struct {
				Part string `json:"part"`
			}
			if err := json.Unmarshal(args, &input); err != nil {
				return "", fmt.Errorf("decode lookup_part args: %w", err)
			}
			note, ok := deps.Parts[input.Part]
			if !ok {
				return "", fmt.Errorf("no part named %q in the workshop", input.Part)
			}
			return note, nil
		},
	})

	agent, err := golem.New[workshop, string](client,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithInstructions[workshop, string]("Use the lookup_part tool before answering questions about parts."),
		golem.WithTools[workshop, string](lookupPart),
	)
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}

	deps := workshop{Parts: map[string]string{
		"memory": "32 GB in stock, shelf B2",
		"servo":  "back-ordered until next week",
	}}
	result, err := agent.Run(context.Background(), golem.RunContext[workshop]{Deps: deps},
		"Can I ship a robot with memory today, and what about servos?")
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
	fmt.Printf("usage: %d input, %d output tokens\n",
		result.Usage.InputTokens, result.Usage.OutputTokens)
}
