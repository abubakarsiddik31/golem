package testmodel_test

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

func TestScriptedPlaysQueuedOutcomesInOrder(t *testing.T) {
	t.Parallel()

	failure := errors.New("429 rate limited")
	m := testmodel.New().
		Respond(model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "first"}}).
		Fail(failure).
		Respond(model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "second"}})

	for i, want := range []struct {
		content string
		err     error
	}{{"first", nil}, {"", failure}, {"second", nil}} {
		response, err := m.Generate(context.Background(), model.Request{})
		if !errors.Is(err, want.err) {
			t.Fatalf("Generate %d error = %v, want %v", i, err, want.err)
		}
		if response.Message.Content != want.content {
			t.Fatalf("Generate %d content = %q, want %q", i, response.Message.Content, want.content)
		}
	}

	// An exhausted queue fails instead of blocking.
	if _, err := m.Generate(context.Background(), model.Request{}); err == nil ||
		!strings.Contains(err.Error(), "no queued outcome") {
		t.Fatalf("exhausted queue error = %v, want the no-outcome failure", err)
	}
}

func TestScriptedRecordsRequests(t *testing.T) {
	t.Parallel()

	m := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "ok"}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "ok"}},
	)
	for _, prompt := range []string{"one", "two"} {
		if _, err := m.Generate(context.Background(), model.Request{
			Messages: []model.Message{{Role: model.RoleUser, Content: prompt}},
		}); err != nil {
			t.Fatalf("Generate(%q) error = %v", prompt, err)
		}
	}

	requests := m.Requests()
	if len(requests) != 2 || requests[0].Messages[0].Content != "one" || requests[1].Messages[0].Content != "two" {
		t.Fatalf("recorded requests = %#v", requests)
	}

	// The recording is a snapshot: mutating the returned slice does not
	// rewrite what the model received.
	requests[0].Messages[0].Content = "rewritten"
	if m.Requests()[0].Messages[0].Content != "one" {
		t.Fatalf("recorded request was rewritten through the returned slice")
	}
}

func TestScriptedStreamReplaysResponseAsFragments(t *testing.T) {
	t.Parallel()

	replay := func() model.Response {
		return model.Response{Message: model.Message{
			Role:    model.RoleAssistant,
			Content: "partial answer",
			ToolCalls: []model.ToolCall{
				{ID: "call-1", Name: "roll", Args: json.RawMessage(`{}`)},
				{ID: "call-2", Name: "roll", Args: json.RawMessage(`{}`)},
			},
		}}
	}
	m := testmodel.New().Respond(replay(), replay())

	var deltas []model.Delta
	response, err := m.GenerateStream(context.Background(), model.Request{},
		func(d model.Delta) error {
			deltas = append(deltas, d)
			return nil
		})
	if err != nil {
		t.Fatalf("GenerateStream() error = %v", err)
	}
	if response.Message.Content != "partial answer" {
		t.Fatalf("response content = %q", response.Message.Content)
	}
	if len(deltas) != 3 {
		t.Fatalf("deltas = %#v", deltas)
	}
	if deltas[0].Content != "partial answer" {
		t.Fatalf("content delta = %#v", deltas[0])
	}
	if deltas[1].ToolCalls[0].Index != 0 || deltas[1].ToolCalls[0].ID != "call-1" ||
		deltas[2].ToolCalls[0].Index != 1 || deltas[2].ToolCalls[0].ID != "call-2" {
		t.Fatalf("tool-call deltas = %#v", deltas[1:])
	}

	// A nil onDelta discards fragments and still returns the response.
	response, err = m.GenerateStream(context.Background(), model.Request{}, nil)
	if err != nil || response.Message.Content != "partial answer" {
		t.Fatalf("nil onDelta: response = %#v, err = %v", response, err)
	}
}

func TestStreamFuncSupportsPlainGeneration(t *testing.T) {
	t.Parallel()

	m := testmodel.StreamFunc(func(ctx context.Context, request model.Request, onDelta func(model.Delta) error) (model.Response, error) {
		if err := testmodel.Emit(onDelta, model.Delta{Content: "word"}); err != nil {
			return model.Response{}, err
		}
		return model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "word"}}, nil
	})

	// Plain generation passes a nil onDelta; Emit keeps the fake working.
	response, err := m.Generate(context.Background(), model.Request{})
	if err != nil || response.Message.Content != "word" {
		t.Fatalf("Generate() = %#v, %v", response, err)
	}
	var streamed []model.Delta
	response, err = m.GenerateStream(context.Background(), model.Request{},
		func(d model.Delta) error {
			streamed = append(streamed, d)
			return nil
		})
	if err != nil || response.Message.Content != "word" || len(streamed) != 1 {
		t.Fatalf("GenerateStream() = %#v, %v, deltas %#v", response, err, streamed)
	}
}

func TestScriptedDrivesAnAgentEndToEnd(t *testing.T) {
	t.Parallel()

	client := testmodel.New().
		Respond(model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "get_player_name", Args: json.RawMessage(`{}`)},
		}}}).
		Respond(model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "Anne wins"}})
	getName := tool.MustNew(tool.Tool[struct{}]{
		Name:        "get_player_name",
		Description: "Get the player's name.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps struct{}, args json.RawMessage) (string, error) {
			return "Anne", nil
		},
	})
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(ctx context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithTools[struct{}, string](getName))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "who wins?")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "Anne wins" {
		t.Fatalf("Output = %q", result.Output)
	}

	// The recording captures exactly what the agent sent: the first
	// request carries the prompt, the second carries the tool result.
	requests := client.Requests()
	if len(requests) != 2 {
		t.Fatalf("recorded requests = %d, want 2", len(requests))
	}
	if last := requests[0].Messages[len(requests[0].Messages)-1]; last.Role != model.RoleUser || last.Content != "who wins?" {
		t.Fatalf("first request tail = %#v", last)
	}
	if last := requests[1].Messages[len(requests[1].Messages)-1]; last.Role != model.RoleTool || last.Content != "Anne" {
		t.Fatalf("second request tail = %#v", last)
	}
}
