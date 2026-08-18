package golem_test

import (
	"bytes"
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

type fakeModel struct {
	ctx      context.Context
	request  model.Request
	response model.Response
	err      error
}

func (m *fakeModel) Generate(ctx context.Context, request model.Request) (model.Response, error) {
	m.ctx = ctx
	m.request = request
	return m.response, m.err
}

type contextKey string

func TestAgentRunBuildsRequestAndPreservesEvidence(t *testing.T) {
	t.Parallel()

	client := &fakeModel{response: model.Response{
		Message: model.Message{Role: model.RoleAssistant, Content: "42"},
		Usage:   model.Usage{InputTokens: 12, OutputTokens: 1},
	}}
	agent, err := golem.New[struct{ Tenant string }, int](
		client,
		golem.DecodeFunc[int](func(ctx context.Context, response model.Response) (int, error) {
			if got := ctx.Value(contextKey("request-id")); got != "run-42" {
				t.Fatalf("decoder context request-id = %v, want run-42", got)
			}
			if response.Message.Content != "42" {
				t.Fatalf("decoder received %q", response.Message.Content)
			}
			return 42, nil
		}),
		golem.WithInstructions[struct{ Tenant string }, int]("Be concise."),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx := context.WithValue(context.Background(), contextKey("request-id"), "run-42")
	result, err := agent.Run(ctx, golem.RunContext[struct{ Tenant string }]{Deps: struct{ Tenant string }{"acme"}}, "What is the answer?")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != 42 {
		t.Fatalf("Output = %d, want 42", result.Output)
	}
	if result.Usage.InputTokens != 12 || len(result.Messages) != 3 {
		t.Fatalf("result evidence = %#v", result)
	}
	if client.ctx != ctx {
		t.Fatal("Run() did not pass the caller context to the model")
	}
	if got, want := client.request.Messages, []model.Message{
		{Role: model.RoleSystem, Content: "Be concise."},
		{Role: model.RoleUser, Content: "What is the answer?"},
	}; len(got) != len(want) ||
		got[0].Role != want[0].Role || got[0].Content != want[0].Content ||
		got[1].Role != want[1].Role || got[1].Content != want[1].Content {
		t.Fatalf("request messages = %#v, want %#v", got, want)
	}
}

func TestNewRejectsMissingRequiredCollaborators(t *testing.T) {
	t.Parallel()

	decoder := golem.DecodeFunc[string](func(context.Context, model.Response) (string, error) {
		return "", nil
	})
	if _, err := golem.New[struct{}, string](nil, decoder); err == nil {
		t.Fatal("New() error = nil, want missing-model error")
	}

	if _, err := golem.New[struct{}, string](&fakeModel{}, nil); err == nil {
		t.Fatal("New() error = nil, want missing-decoder error")
	}
}

func TestAgentRunClassifiesModelAndDecodeFailures(t *testing.T) {
	t.Parallel()

	modelFailure := errors.New("provider unavailable")
	decodeFailure := errors.New("invalid response")
	tests := []struct {
		name   string
		model  *fakeModel
		decode golem.DecodeFunc[string]
		stage  golem.Stage
		cause  error
	}{
		{
			name:   "model",
			model:  &fakeModel{err: modelFailure},
			decode: func(context.Context, model.Response) (string, error) { return "", nil },
			stage:  golem.StageModel,
			cause:  modelFailure,
		},
		{
			name:   "decode",
			model:  &fakeModel{response: model.Response{}},
			decode: func(context.Context, model.Response) (string, error) { return "", decodeFailure },
			stage:  golem.StageDecode,
			cause:  decodeFailure,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			agent, err := golem.New[struct{}, string](test.model, test.decode)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "hello")
			var runErr *golem.RunError
			if !errors.As(err, &runErr) || runErr.Stage != test.stage {
				t.Fatalf("Run() error = %v, want stage %q", err, test.stage)
			}
			if !errors.Is(err, test.cause) {
				t.Fatalf("Run() error = %v, want cause %v", err, test.cause)
			}
		})
	}
}

func TestRunWithPromptPartsAttachesPartsToUserMessage(t *testing.T) {
	t.Parallel()

	png := []byte{1, 2, 3}
	client := &fakeModel{response: model.Response{
		Message: model.Message{Role: model.RoleAssistant, Content: "a red square"},
	}}
	agent, err := golem.New[struct{}, string](
		client,
		golem.DecodeFunc[string](func(ctx context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{},
		"describe these",
		golem.WithPromptImageURL("https://example.com/a.png"),
		golem.WithPromptImageData("image/png", png))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	last := client.request.Messages[len(client.request.Messages)-1]
	if last.Role != model.RoleUser || last.Content != "describe these" {
		t.Fatalf("prompt message malformed: %#v", last)
	}
	if len(last.Parts) != 2 {
		t.Fatalf("prompt carries %d parts, want 2", len(last.Parts))
	}
	if last.Parts[0].Kind != model.PartImage || last.Parts[0].URL != "https://example.com/a.png" || len(last.Parts[0].Data) != 0 {
		t.Fatalf("part[0] = %#v, want the URL image", last.Parts[0])
	}
	if last.Parts[1].MediaType != "image/png" || !bytes.Equal(last.Parts[1].Data, png) {
		t.Fatalf("part[1] = %#v", last.Parts[1])
	}
	if result.Messages[len(result.Messages)-2].Parts == nil {
		t.Fatal("result evidence lost the prompt parts")
	}
}

func TestRunWithMalformedPromptPartFailsBeforeAnyModelCall(t *testing.T) {
	t.Parallel()

	client := &fakeModel{response: model.Response{
		Message: model.Message{Role: model.RoleAssistant, Content: "unused"},
	}}
	agent, err := golem.New[struct{}, string](
		client,
		golem.DecodeFunc[string](func(ctx context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "hi",
		golem.WithPromptParts(model.Part{Kind: model.PartImage}))
	if err == nil {
		t.Fatal("malformed part accepted")
	}
	if !strings.Contains(err.Error(), "prompt part 0") {
		t.Fatalf("Run() error = %v, want it to name prompt part 0", err)
	}
	if client.request.Messages != nil {
		t.Fatal("model was called despite an invalid part")
	}
}

func TestRunWithHistoryRejectsPartsOutsideUserMessages(t *testing.T) {
	t.Parallel()

	client := &fakeModel{response: model.Response{
		Message: model.Message{Role: model.RoleAssistant, Content: "ok"},
	}}
	agent, err := golem.New[struct{}, string](
		client,
		golem.DecodeFunc[string](func(ctx context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	history := []model.Message{{Role: model.RoleAssistant, Content: "hi",
		Parts: []model.Part{model.ImageURL("https://example.com/a.png")}}}
	_, err = agent.RunWithHistory(context.Background(), golem.RunContext[struct{}]{}, history, "go on")
	if err == nil || !strings.Contains(err.Error(), "user messages") {
		t.Fatalf("RunWithHistory() error = %v, want rejection of non-user parts", err)
	}
	if client.request.Messages != nil {
		t.Fatal("model was called despite invalid history parts")
	}
}

// usageEchoTool is a minimal tool the usage-bound tests execute.
func usageEchoTool(t *testing.T) tool.Tool[struct{}] {
	t.Helper()
	return tool.MustNew(tool.Tool[struct{}]{
		Name:        "echo",
		Description: "Echo the arguments.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, deps struct{}, args json.RawMessage) (string, error) {
			return string(args), nil
		},
	})
}

func TestUsageLimitBoundsRequests(t *testing.T) {
	t.Parallel()

	client := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{ID: "c1", Name: "echo", Args: json.RawMessage(`{}`)}}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "done"}},
	)
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(ctx context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithTools[struct{}, string](usageEchoTool(t)),
		golem.WithUsageLimit[struct{}, string](golem.UsageLimit{Requests: 1}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	var runErr *golem.RunError
	var limitErr *golem.UsageLimitError
	if !errors.As(err, &runErr) || runErr.Stage != golem.StageUsage ||
		!errors.As(err, &limitErr) || limitErr.Kind != "request" || limitErr.Limit != 1 || limitErr.Actual != 2 {
		t.Fatalf("Run() error = %v, want the usage stage with a crossed request bound", err)
	}
}

func TestUsageLimitBoundsToolCalls(t *testing.T) {
	t.Parallel()

	client := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{ID: "c1", Name: "echo", Args: json.RawMessage(`{}`)},
				{ID: "c2", Name: "echo", Args: json.RawMessage(`{}`)},
			}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "done"}},
	)
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(ctx context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithTools[struct{}, string](usageEchoTool(t)),
		golem.WithUsageLimit[struct{}, string](golem.UsageLimit{ToolCalls: 1}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go")
	var limitErr *golem.UsageLimitError
	if !errors.As(err, &limitErr) || limitErr.Kind != "tool call" || limitErr.Limit != 1 || limitErr.Actual != 2 {
		t.Fatalf("Run() error = %v, want a crossed tool-call bound", err)
	}
}

func TestUsageLimitRequestsWithinBoundSucceeds(t *testing.T) {
	t.Parallel()

	client := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "done"}},
	)
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(ctx context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithUsageLimit[struct{}, string](golem.UsageLimit{Requests: 2, ToolCalls: 1}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go"); err != nil {
		t.Fatalf("Run() error = %v, want success within the bounds", err)
	}
}

func TestNewRejectsNegativeActivityBounds(t *testing.T) {
	t.Parallel()

	for _, limit := range []golem.UsageLimit{
		{Requests: -1},
		{ToolCalls: -1},
	} {
		if _, err := golem.New[struct{}, string](&fakeModel{},
			golem.DecodeFunc[string](func(ctx context.Context, response model.Response) (string, error) {
				return response.Message.Content, nil
			}),
			golem.WithUsageLimit[struct{}, string](limit)); err == nil {
			t.Fatalf("New() accepted negative limit %+v", limit)
		}
	}
}
