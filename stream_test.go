package golem_test

import (
	"context"
	"errors"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
)

// streamQueuedModel streams queuedModel's queue: it replays the same
// responses, emitting one delta per response — the tool-call fragment for
// a tool turn, the content for a text turn.
type streamQueuedModel struct{ queuedModel }

func (m *streamQueuedModel) GenerateStream(ctx context.Context, request model.Request, onDelta func(model.Delta) error) (model.Response, error) {
	response, err := m.Generate(ctx, request)
	if err != nil {
		return model.Response{}, err
	}
	if onDelta == nil {
		return response, nil
	}
	if response.Message.Content != "" {
		if err := onDelta(model.Delta{Content: response.Message.Content}); err != nil {
			return model.Response{}, err
		}
	}
	for _, call := range response.Message.ToolCalls {
		delta := model.Delta{ToolCalls: []model.ToolCallDelta{{Index: 0, ID: call.ID, Name: call.Name}}}
		if err := onDelta(delta); err != nil {
			return model.Response{}, err
		}
	}
	return response, nil
}

func streamClient(responses ...model.Response) *streamQueuedModel {
	return &streamQueuedModel{queuedModel{responses: responses}}
}

func TestAgentRunStreamStreamsAcrossToolTurns(t *testing.T) {
	t.Parallel()

	client := streamClient(
		rollCall("call-1", `{"n":4}`),
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "settled"}, Usage: model.Usage{InputTokens: 6, OutputTokens: 3}},
	)
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithTools[struct{}, string](rollTool(t)))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	var deltas []model.Delta
	result, err := agent.RunStream(context.Background(), golem.RunContext[struct{}]{}, "roll a 4",
		func(d model.Delta) error {
			deltas = append(deltas, d)
			return nil
		})
	if err != nil {
		t.Fatalf("RunStream() error = %v", err)
	}

	// The run itself is unchanged: typed output, executed tool, full
	// evidence, summed usage.
	if result.Output != "settled" {
		t.Fatalf("Output = %q", result.Output)
	}
	if result.Usage != (model.Usage{InputTokens: 6, OutputTokens: 3}) {
		t.Fatalf("Usage = %#v", result.Usage)
	}
	wantRoles := []model.Role{model.RoleUser, model.RoleAssistant, model.RoleTool, model.RoleAssistant}
	if len(result.Messages) != len(wantRoles) {
		t.Fatalf("evidence = %#v", result.Messages)
	}
	for i, role := range wantRoles {
		if result.Messages[i].Role != role {
			t.Fatalf("evidence[%d].Role = %q, want %q", i, result.Messages[i].Role, role)
		}
	}

	// Fragments arrive across the tool boundary, in turn order.
	if len(deltas) != 2 {
		t.Fatalf("deltas = %#v, want 2", deltas)
	}
	if fragment := deltas[0].ToolCalls[0]; fragment.ID != "call-1" || fragment.Name != "roll" {
		t.Fatalf("first delta = %#v, want the tool-call fragment", deltas[0])
	}
	if deltas[1].Content != "settled" {
		t.Fatalf("second delta = %#v, want the streamed answer", deltas[1])
	}
}

func TestAgentRunStreamRestreamsCorrectionRounds(t *testing.T) {
	t.Parallel()

	t.Run("output correction", func(t *testing.T) {
		t.Parallel()

		client := streamClient(
			model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "almost"}},
			model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "exact"}},
		)
		agent, err := golem.New[struct{}, string](client, strictDecoder("exact"),
			golem.WithOutputRetries[struct{}, string](1))
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		var contents []string
		result, err := agent.RunStream(context.Background(), golem.RunContext[struct{}]{}, "answer exactly",
			func(d model.Delta) error {
				contents = append(contents, d.Content)
				return nil
			})
		if err != nil {
			t.Fatalf("RunStream() error = %v", err)
		}
		if result.Output != "exact" {
			t.Fatalf("Output = %q", result.Output)
		}
		if len(contents) != 2 || contents[0] != "almost" || contents[1] != "exact" {
			t.Fatalf("streamed contents = %#v, want the rejected and corrected answers", contents)
		}
	})

	t.Run("tool rejection", func(t *testing.T) {
		t.Parallel()

		client := streamClient(
			rollCall("call-1", `{"n":0}`),
			rollCall("call-2", `{"n":4}`),
			model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "settled"}},
		)
		agent, err := golem.New[struct{}, string](client, decoderOf(),
			golem.WithTools[struct{}, string](rollTool(t)),
			golem.WithToolRetries[struct{}, string](1))
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		var fragments []string
		_, err = agent.RunStream(context.Background(), golem.RunContext[struct{}]{}, "roll a 4",
			func(d model.Delta) error {
				if d.Content != "" {
					fragments = append(fragments, d.Content)
				}
				return nil
			})
		if err != nil {
			t.Fatalf("RunStream() error = %v", err)
		}
		if len(fragments) != 1 || fragments[0] != "settled" {
			t.Fatalf("streamed contents = %#v, want only the final answer", fragments)
		}
		// Both tool-call turns streamed their fragments around the
		// locally produced rejection and result messages.
		if len(client.requests) != 3 {
			t.Fatalf("model turns = %d, want 3", len(client.requests))
		}
	})
}

func TestAgentRunStreamRequiresStreamingModel(t *testing.T) {
	t.Parallel()

	agent, err := golem.New[struct{}, string](&queuedModel{}, decoderOf())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.RunStream(context.Background(), golem.RunContext[struct{}]{}, "hi", nil)
	if err == nil {
		t.Fatal("RunStream() error = nil, want streaming capability error")
	}
	var runErr *golem.RunError
	if errors.As(err, &runErr) {
		t.Fatalf("RunStream() error = %v, capability mismatch must not be a RunError", err)
	}
}

func TestAgentRunStreamSurfacesCallerStopAtModelStage(t *testing.T) {
	t.Parallel()

	stop := errors.New("stop listening")
	client := streamClient(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "word"}},
	)
	agent, err := golem.New[struct{}, string](client, decoderOf())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.RunStream(context.Background(), golem.RunContext[struct{}]{}, "hi",
		func(model.Delta) error { return stop })
	var runErr *golem.RunError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageModel {
		t.Fatalf("RunStream() error = %v, want model stage", err)
	}
	if !errors.Is(err, stop) {
		t.Fatalf("RunStream() error = %v, want the caller's stop error as cause", err)
	}
	if len(client.requests) != 1 {
		t.Fatalf("model turns = %d, want 1", len(client.requests))
	}
}
