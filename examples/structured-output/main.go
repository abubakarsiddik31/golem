// Command structured-output extracts a typed value: the agent declares a
// JSON Schema the adapter sends as structured-output instructions, and
// DecodeJSON turns the response content into the declared type.
//
// Set OPENAI_API_KEY (and optionally OPENAI_MODEL) to run it.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/providers"
	"github.com/abubakarsiddik31/golem/providers/openai"
)

type weather struct {
	City    string `json:"city"`
	Celsius int    `json:"celsius"`
	Summary string `json:"summary"`
}

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

	// Temperature 0 favors deterministic extraction; nil would leave the
	// provider default. providers.Ptr sets the value, including 0.
	client, err := openai.New(openai.Config{
		APIKey:      apiKey,
		Model:       modelName,
		Temperature: providers.Ptr(0.0),
	})
	if err != nil {
		fmt.Println("openai.New:", err)
		return
	}

	agent, err := golem.New[struct{}, weather](client, golem.DecodeJSON[weather](),
		golem.WithInstructions[struct{}, weather]("Report plausible sample weather; do not hedge."),
		golem.WithOutputSchema[struct{}, weather](json.RawMessage(`{
			"type": "object",
			"properties": {
				"city": {"type": "string"},
				"celsius": {"type": "integer"},
				"summary": {"type": "string"}
			},
			"required": ["city", "celsius", "summary"],
			"additionalProperties": false
		}`)),
	)
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{},
		"Weather forecast for Lagos, Nigeria.")
	if err != nil {
		fmt.Println("Run:", err)
		return
	}
	fmt.Printf("%s: %d°C — %s\n", result.Output.City, result.Output.Celsius, result.Output.Summary)
}
