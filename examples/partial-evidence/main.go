// Command partial-evidence shows a failed run keeping its evidence: a
// model failure after a completed tool turn carries RunError.Partial —
// the conversation so far, the usage completed turns reported, and the
// activity counts — and the partial messages resume through
// RunWithHistory. It runs against a scripted fake model — no network,
// no credentials, fully deterministic.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
	"github.com/abubakarsiddik31/golem/tool"
)

func main() {
	lookup := tool.MustNew(tool.Tool[struct{}]{
		Name:        "lookup",
		Description: "Look up a fact.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps struct{}, args json.RawMessage) (string, error) {
			return "golem agents preserve evidence", nil
		},
	})
	decoder := golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
		return response.Message.Content, nil
	})

	// First run: the model requests the tool, the tool answers, then the
	// provider fails on the next turn.
	failing := testmodel.New().Respond(
		model.Response{
			Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
				{ID: "call-1", Name: "lookup", Args: json.RawMessage(`{}`)},
			}},
			Usage: model.Usage{InputTokens: 12, OutputTokens: 4},
		},
	).Fail(errors.New("provider unavailable"))
	agent, err := golem.New[struct{}, string](failing, decoder,
		golem.WithTools[struct{}, string](lookup))
	if err != nil {
		log.Fatal("golem.New: ", err)
	}

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "what preserves evidence?")
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Partial == nil {
		log.Fatal("Run: expected a RunError carrying partial evidence, got: ", err)
	}
	partial := runErr.Partial
	fmt.Printf("run failed at the %s stage: %v\n", runErr.Stage, err)
	fmt.Printf("evidence so far: %d messages, %d requests, %d tool calls, %d input tokens\n",
		len(partial.Messages), partial.Requests, partial.ToolCalls, partial.Usage.InputTokens)

	// Resume: the partial conversation continues with a healthy model.
	resumed := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "golem runs do — even failed ones"}})
	resumeAgent, err := golem.New[struct{}, string](resumed, decoder,
		golem.WithTools[struct{}, string](lookup))
	if err != nil {
		log.Fatal("golem.New: ", err)
	}
	result, err := resumeAgent.RunWithHistory(context.Background(),
		golem.RunContext[struct{}]{}, partial.Messages, "answer with what you have")
	if err != nil {
		log.Fatal("RunWithHistory: ", err)
	}
	fmt.Println("resumed output:", result.Output)
}
