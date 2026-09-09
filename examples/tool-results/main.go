// Command tool-results shows tools returning more than text: one tool
// hands back an image as evidence beside its result, and a definitive
// failure reaches the model as the tool's result — without consuming the
// tool's retry budget.
//
// The run is fully scripted — no network, no credentials. The same tools
// behave identically against a live provider: each adapter places the
// parts where its API carries them (inside the tool result, or framed on
// the user channel) and reports failures through the provider's native
// channel.
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
	"github.com/abubakarsiddik31/golem/tool"
)

// screenshot captures a screen and returns the capture as evidence beside
// a short text summary.
func screenshot() tool.Tool[struct{}] {
	return tool.MustNew(tool.Tool[struct{}]{
		Name:        "screenshot",
		Description: "Capture the screen.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"target":{"type":"string"}}}`),
		Exec: func(ctx context.Context, deps struct{}, args json.RawMessage) (tool.Result, error) {
			return tool.Result{
				Text:  "captured the settings dialog",
				Parts: []model.Part{model.ImageData("image/png", []byte{137, 80, 78, 71})},
			}, nil
		},
	})
}

// archive archives a mailbox; this one cannot, definitively.
func archive() tool.Tool[struct{}] {
	return tool.MustNew(tool.Tool[struct{}]{
		Name:        "archive_mailbox",
		Description: "Archive a mailbox.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"mailbox":{"type":"string"}}}`),
		Exec: func(ctx context.Context, deps struct{}, args json.RawMessage) (tool.Result, error) {
			return tool.Result{}, &tool.Failed{Reason: "mailbox projects/2026 is read-only"}
		},
	})
}

func decoder() golem.DecodeFunc[string] {
	return golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
		return response.Message.Content, nil
	})
}

func main() {
	// Turn one requests both tools; turn two answers from their results.
	client := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "screenshot", Args: json.RawMessage(`{"target":"settings"}`)},
			{ID: "call-2", Name: "archive_mailbox", Args: json.RawMessage(`{"mailbox":"projects/2026"}`)},
		}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant,
			Content: "I captured the settings dialog; the mailbox could not be archived because it is read-only."}},
	)

	agent, err := golem.New[struct{}, string](client, decoder(),
		golem.WithTools[struct{}, string](screenshot(), archive()),
		golem.WithToolRetries[struct{}, string](3),
	)
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{},
		"capture the settings dialog and archive projects/2026")
	if err != nil {
		fmt.Println("Run:", err)
		return
	}

	fmt.Println("output:", result.Output)
	for _, message := range result.Messages {
		if message.Role != model.RoleTool {
			continue
		}
		status := "ok"
		if message.Failed {
			status = "failed"
		}
		fmt.Printf("tool %s (call %s): %q [%s], %d part(s)\n",
			message.ToolName, message.ToolCallID, message.Content, status, len(message.Parts))
	}
}
