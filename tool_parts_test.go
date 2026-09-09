package golem_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
	"github.com/abubakarsiddik31/golem/tool"
)

// camera captures one screenshot and returns it as tool evidence.
func camera(t *testing.T) tool.Tool[struct{}] {
	t.Helper()
	return tool.MustNew(tool.Tool[struct{}]{
		Name:        "camera",
		Description: "Take a screenshot.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps struct{}, args json.RawMessage) (tool.Result, error) {
			return tool.Result{
				Text: "screenshot taken",
				Parts: []model.Part{
					model.ImageData("image/png", []byte{1, 2, 3}),
					model.DocumentData("application/pdf", []byte{4, 5}),
				},
			}, nil
		},
	})
}

func testCameraAgent(t *testing.T, responses ...model.Response) (*testmodel.Scripted, *golem.Agent[struct{}, string]) {
	t.Helper()
	client := testmodel.New().Respond(responses...)
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) {
			return r.Message.Content, nil
		}),
		golem.WithTools[struct{}, string](camera(t)))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return client, agent
}

func testToolCallResponse(id, name string) model.Response {
	return model.Response{Message: model.Message{Role: model.RoleAssistant,
		ToolCalls: []model.ToolCall{{ID: id, Name: name, Args: json.RawMessage(`{}`)}}}}
}

func TestToolResultCarriesParts(t *testing.T) {
	t.Parallel()

	_, agent := testCameraAgent(t,
		testToolCallResponse("c1", "camera"),
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "here is your screenshot"}})

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "take a look")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	var toolMessage *model.Message
	for i := range result.Messages {
		if result.Messages[i].Role == model.RoleTool {
			toolMessage = &result.Messages[i]
		}
	}
	if toolMessage == nil {
		t.Fatal("no tool message in the evidence")
	}
	if toolMessage.Content != "screenshot taken" {
		t.Fatalf("tool content = %q", toolMessage.Content)
	}
	if len(toolMessage.Parts) != 2 || toolMessage.Parts[0].Kind != model.PartImage ||
		toolMessage.Parts[1].Kind != model.PartDocument {
		t.Fatalf("tool parts = %#v, want the image and document the tool produced", toolMessage.Parts)
	}
	// The parts ride the durable JSON like any other part.
	encoded, err := json.Marshal(toolMessage)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(encoded), `"parts"`) {
		t.Fatalf("tool message JSON = %s, want the parts encoded", encoded)
	}
}

func TestToolFailedIsRecordedWithoutConsumingRetries(t *testing.T) {
	t.Parallel()

	attempts := 0
	dead := tool.MustNew(tool.Tool[struct{}]{
		Name:        "archive",
		Description: "Archive a mailbox.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps struct{}, args json.RawMessage) (tool.Result, error) {
			attempts++
			return tool.Result{}, &tool.Failed{Reason: "mailbox is read-only"}
		},
	})
	client := testmodel.New().Respond(
		testToolCallResponse("c1", "archive"),
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "the mailbox cannot be archived"}})
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) {
			return r.Message.Content, nil
		}),
		golem.WithTools[struct{}, string](dead),
		golem.WithToolRetries[struct{}, string](3))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "archive it")
	if err != nil {
		t.Fatalf("Run() error = %v, want the failure to reach the model and the run to continue", err)
	}
	if result.Output != "the mailbox cannot be archived" {
		t.Fatalf("Output = %q, want the run to continue past the failure", result.Output)
	}
	if attempts != 1 {
		t.Fatalf("tool ran %d times, want 1 — a definitive failure consumes no retry budget", attempts)
	}
	var toolMessage *model.Message
	for i := range result.Messages {
		if result.Messages[i].Role == model.RoleTool {
			toolMessage = &result.Messages[i]
		}
	}
	if toolMessage == nil || !toolMessage.Failed || toolMessage.Content != "mailbox is read-only" {
		t.Fatalf("tool message = %#v, want the failure recorded as the result", toolMessage)
	}
}

func TestToolFailedJSONStaysAdditive(t *testing.T) {
	t.Parallel()

	message := model.Message{Role: model.RoleTool, ToolCallID: "c1", ToolName: "t",
		Content: "no such file", Failed: true}
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if want := `"failed":true`; !strings.Contains(string(encoded), want) {
		t.Fatalf("tool message JSON = %s, want %s", encoded, want)
	}
	var decoded model.Message
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !decoded.Failed || decoded.Content != "no such file" {
		t.Fatalf("decoded = %#v", decoded)
	}
}

func TestOtherToolErrorsStillFailTheRun(t *testing.T) {
	t.Parallel()

	boom := errors.New("disk on fire")
	fatal := tool.MustNew(tool.Tool[struct{}]{
		Name:        "fatal",
		Description: "Fails hard.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps struct{}, args json.RawMessage) (tool.Result, error) {
			return tool.Result{}, boom
		},
	})
	client := testmodel.New().Respond(testToolCallResponse("c1", "fatal"))
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) {
			return r.Message.Content, nil
		}),
		golem.WithTools[struct{}, string](fatal))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want an unclassified tool error to fail the run", err)
	}
}

func TestToolPartEventCarriesParts(t *testing.T) {
	t.Parallel()

	client, agent := testCameraAgent(t,
		testToolCallResponse("c1", "camera"),
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "done"}})
	var sawParts []model.Part
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) {
			return r.Message.Content, nil
		}),
		golem.WithTools[struct{}, string](camera(t)),
		golem.WithRunEvents[struct{}, string](func(e golem.RunEvent) {
			if e.Kind == golem.EventToolEnd && len(e.Parts) > 0 {
				sawParts = e.Parts
			}
		}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(sawParts) != 2 {
		t.Fatalf("tool-end event parts = %#v, want the tool's evidence", sawParts)
	}
}

func TestMalformedToolPartFailsTheRun(t *testing.T) {
	t.Parallel()

	bad := tool.MustNew(tool.Tool[struct{}]{
		Name:        "bad",
		Description: "Produces a malformed part.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps struct{}, args json.RawMessage) (tool.Result, error) {
			return tool.Result{Parts: []model.Part{model.ImageData("", []byte{1})}}, nil
		},
	})
	client := testmodel.New().Respond(testToolCallResponse("c1", "bad"))
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) {
			return r.Message.Content, nil
		}),
		golem.WithTools[struct{}, string](bad))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageTool ||
		!strings.Contains(err.Error(), "invalid part") {
		t.Fatalf("error = %v, want a tool-stage failure naming the malformed part", err)
	}
}

func TestHistoryAllowsPartsOnToolMessages(t *testing.T) {
	t.Parallel()

	client := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "seen"}})
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) {
			return r.Message.Content, nil
		}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	history := []model.Message{
		{Role: model.RoleUser, Content: "look"},
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "c1", Name: "camera"}}},
		{Role: model.RoleTool, ToolCallID: "c1", ToolName: "camera", Content: "screenshot",
			Parts: []model.Part{model.ImageData("image/png", []byte{1})}},
	}
	if _, err := agent.RunWithHistory(context.Background(), golem.RunContext[struct{}]{}, history, "well?"); err != nil {
		t.Fatalf("RunWithHistory() error = %v, want tool-message parts to validate", err)
	}
}
