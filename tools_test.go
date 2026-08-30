package golem_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

// queuedModel returns scripted responses in order and records requests.
type queuedModel struct {
	requests  []model.Request
	responses []model.Response
}

func (m *queuedModel) Generate(ctx context.Context, request model.Request) (model.Response, error) {
	m.requests = append(m.requests, request)
	if len(m.responses) == 0 {
		return model.Response{}, errors.New("queuedModel: no queued response")
	}
	response := m.responses[0]
	m.responses = m.responses[1:]
	return response, nil
}

type playerDeps struct{ Name string }

func TestAgentRunExecutesToolsWithTypedDeps(t *testing.T) {
	t.Parallel()

	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "get_player_name", Args: json.RawMessage(`{}`)},
		}}},
		{Message: model.Message{Role: model.RoleAssistant, Content: "Anne wins"}, Usage: model.Usage{InputTokens: 5, OutputTokens: 2}},
	}}
	getName := tool.MustNew(tool.Tool[playerDeps]{
		Name:        "get_player_name",
		Description: "Get the player's name.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps playerDeps, args json.RawMessage) (string, error) {
			return deps.Name, nil
		},
	})

	agent, err := golem.New[playerDeps, string](
		client,
		golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) {
			return r.Message.Content, nil
		}),
		golem.WithInstructions[playerDeps, string]("Run the game."),
		golem.WithTools[playerDeps, string](getName),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[playerDeps]{Deps: playerDeps{Name: "Anne"}}, "My guess is 4")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "Anne wins" {
		t.Fatalf("Output = %q", result.Output)
	}
	if result.Usage != (model.Usage{InputTokens: 5, OutputTokens: 2}) {
		t.Fatalf("Usage = %#v", result.Usage)
	}

	// The model's first request advertises the tool.
	if len(client.requests) != 2 {
		t.Fatalf("model turns = %d, want 2", len(client.requests))
	}
	first := client.requests[0]
	if len(first.ToolSpecs) != 1 || first.ToolSpecs[0].Name != "get_player_name" {
		t.Fatalf("tool specs = %#v", first.ToolSpecs)
	}
	// The second request carries the tool result.
	second := client.requests[1]
	if len(second.Messages) != 4 {
		t.Fatalf("second request messages = %#v", second.Messages)
	}
	toolResult := second.Messages[3]
	if toolResult.Role != model.RoleTool || toolResult.ToolName != "get_player_name" || toolResult.Content != "Anne" {
		t.Fatalf("tool result message = %#v", toolResult)
	}
	// Result evidence keeps the whole exchange in order:
	// system, user, assistant call, tool result, final answer.
	if len(result.Messages) != 5 || result.Messages[4].Content != "Anne wins" {
		t.Fatalf("result messages = %#v", result.Messages)
	}
}

func TestAgentRunClassifiesToolAndLoopFailures(t *testing.T) {
	t.Parallel()

	cause := errors.New("tool exploded")
	decoder := golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) {
		return r.Message.Content, nil
	})
	callTo := func(name, id string) model.Response {
		return model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: id, Name: name, Args: json.RawMessage(`{}`)},
		}}}
	}

	t.Run("tool failure aborts with tool stage", func(t *testing.T) {
		t.Parallel()

		failing := tool.MustNew(tool.Tool[struct{}]{
			Name:        "failing",
			Description: "Always fails.",
			Schema:      json.RawMessage(`{"type":"object"}`),
			Exec: func(context.Context, struct{}, json.RawMessage) (string, error) {
				return "", cause
			},
		})
		agent, err := golem.New[struct{}, string](&queuedModel{responses: []model.Response{callTo("failing", "c1")}},
			decoder, golem.WithTools[struct{}, string](failing))
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
		var runErr *golem.RunError
		if !errors.As(err, &runErr) || runErr.Stage != golem.StageTool {
			t.Fatalf("Run() error = %v, want tool stage", err)
		}
		if !errors.Is(err, cause) {
			t.Fatalf("Run() error = %v, want cause %v", err, cause)
		}
	})

	t.Run("turn limit aborts with loop stage", func(t *testing.T) {
		t.Parallel()

		succeeding := tool.MustNew(tool.Tool[struct{}]{
			Name:        "looping",
			Description: "Succeeds so the model keeps calling.",
			Schema:      json.RawMessage(`{"type":"object"}`),
			Exec:        func(context.Context, struct{}, json.RawMessage) (string, error) { return "ok", nil },
		})
		agent, err := golem.New[struct{}, string](
			&queuedModel{responses: []model.Response{callTo("looping", "c1"), callTo("looping", "c2"), callTo("looping", "c3")}},
			decoder,
			golem.WithTools[struct{}, string](succeeding),
			golem.WithMaxIterations[struct{}, string](2),
		)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
		var runErr *golem.RunError
		if !errors.As(err, &runErr) || runErr.Stage != golem.StageLoop {
			t.Fatalf("Run() error = %v, want loop stage", err)
		}
	})
}

func TestAgentRunWrapsCancellationWithStage(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	agent, err := golem.New[struct{}, string](&queuedModel{}, decoderOf())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.Run(ctx, golem.RunContext[struct{}]{}, "hello")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled through the chain", err)
	}
	var runErr *golem.RunError
	if !errors.As(err, &runErr) {
		t.Fatalf("Run() error = %v, cancellation must ride a RunError to carry its stage", err)
	}
	if runErr.Stage != golem.StageModel {
		t.Fatalf("RunError stage = %s, want model", runErr.Stage)
	}
	if runErr.Partial != nil {
		t.Fatalf("RunError partial = %+v, want nil: the run died before any activity", runErr.Partial)
	}
}

func TestNewRejectsInvalidToolAndIterationConfiguration(t *testing.T) {
	t.Parallel()

	decoder := decoderOf()
	client := &fakeModel{}
	validTool := tool.Tool[struct{}]{
		Name:        "ok",
		Description: "d",
		Schema:      json.RawMessage(`{}`),
		Exec:        func(context.Context, struct{}, json.RawMessage) (string, error) { return "", nil },
	}

	if _, err := golem.New[struct{}, string](client, decoder,
		golem.WithMaxIterations[struct{}, string](0)); err == nil {
		t.Fatal("New() error = nil, want max-iterations rejection")
	}
	if _, err := golem.New[struct{}, string](client, decoder,
		golem.WithTools[struct{}, string](tool.Tool[struct{}]{Schema: []byte(`{}`)})); err == nil {
		t.Fatal("New() error = nil, want nameless-tool rejection")
	}
	if _, err := golem.New[struct{}, string](client, decoder,
		golem.WithTools[struct{}, string](validTool, validTool)); err == nil {
		t.Fatal("New() error = nil, want duplicate-tool rejection")
	}
	if _, err := golem.New[struct{}, string](client, decoder,
		golem.WithTools[struct{}, string](tool.Tool[struct{}]{Name: "noexec", Schema: []byte(`{}`)})); err == nil {
		t.Fatal("New() error = nil, want missing-exec rejection")
	}
}

func decoderOf() golem.DecodeFunc[string] {
	return func(_ context.Context, r model.Response) (string, error) {
		return r.Message.Content, nil
	}
}
