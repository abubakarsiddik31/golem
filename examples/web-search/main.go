// Command web-search gives an agent the web_search tool and runs it
// offline against a fake backend; a real run uses the keyless DuckDuckGo
// default or the application's own Backend.
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
	"github.com/abubakarsiddik31/golem/websearch"
)

type backend struct{}

func (backend) Search(_ context.Context, query string, _ int) ([]websearch.Result, error) {
	return []websearch.Result{
		{Title: query + " — overview", URL: "https://example.com/overview", Snippet: "A short overview of the topic."},
		{Title: query + " — reference", URL: "https://example.com/reference", Snippet: "The detailed reference."},
	}, nil
}

func main() {
	search := websearch.MustNew[struct{}](websearch.Config{Backend: backend{}})

	client := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{
			ID:   "call-1",
			Name: websearch.ToolName,
			Args: json.RawMessage(`{"query":"Go agent frameworks","limit":2}`),
		}}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "Two results found; the overview is the place to start."}},
	)
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithInstructions[struct{}, string]("Search before answering, then summarize the result with webfetch left to the caller."),
		golem.WithTools[struct{}, string](search),
	)
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "What should I read about Go agent frameworks?")
	if err != nil {
		fmt.Println("Run:", err)
		return
	}

	fmt.Println("answer:", result.Output)
	for _, message := range result.Messages {
		if message.Role == model.RoleTool {
			fmt.Printf("tool %s returned:\n%s\n", message.ToolName, message.Content)
		}
	}
}
