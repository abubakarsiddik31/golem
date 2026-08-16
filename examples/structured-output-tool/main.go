// Command structured-output-tool extracts a typed value through tool-mode
// structured output: the schema becomes the parameters of a synthesized
// output tool, and the run ends on the model's first call to it. This mode
// works with any model that supports tool calling, including those without
// native JSON-schema output.
//
// Set OPENAI_API_KEY (and optionally OPENAI_MODEL) to run it.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/abubakarsiddik31/golem"
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

	client, err := openai.New(openai.Config{APIKey: apiKey, Model: modelName})
	if err != nil {
		fmt.Println("openai.New:", err)
		return
	}

	agent, err := golem.New[struct{}, weather](client, golem.DecodeJSON[weather](),
		golem.WithInstructions[struct{}, weather]("Report plausible sample weather; do not hedge."),
		golem.WithOutputTool[struct{}, weather]("record_weather",
			"Record the final weather report.", json.RawMessage(`{
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
