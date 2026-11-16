package golem_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

func TestPromptContextOrderingIdentityAndDeps(t *testing.T) {
	t.Parallel()

	type deps struct{ User string }
	client := &fakeModel{response: model.Response{
		Message: model.Message{Role: model.RoleAssistant, Content: "ok"},
	}}
	agent, err := golem.New[deps, string](client, decoderOf(),
		golem.WithInstructions[deps, string]("system"),
		golem.WithPromptContext[deps, string](func(_ context.Context, runCtx golem.RunContext[deps]) ([]model.Message, error) {
			return []model.Message{{Role: model.RoleUser, Content: "memory for " + runCtx.Deps.User}}, nil
		}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	history := []model.Message{
		{Role: model.RoleUser, Content: "old question"},
		{Role: model.RoleAssistant, Content: "old answer"},
	}
	result, err := agent.RunWithHistory(context.Background(), golem.RunContext[deps]{Deps: deps{User: "ada"}},
		history, "new question")
	if err != nil {
		t.Fatalf("RunWithHistory() error = %v", err)
	}

	request := client.request.Messages
	want := []string{"system", "old question", "old answer", "memory for ada", "new question"}
	if len(request) != len(want) {
		t.Fatalf("request messages = %#v", request)
	}
	for i, content := range want {
		if request[i].Content != content {
			t.Fatalf("request[%d] = %q, want %q", i, request[i].Content, content)
		}
	}
	if request[3].Role != model.RoleUser || request[3].RunID != result.RunID || request[3].ConversationID != result.ConversationID {
		t.Fatalf("context identity = %#v, result identity %#v", request[3], result)
	}
	if len(result.Messages) != 6 || result.Messages[3].Content != "memory for ada" {
		t.Fatalf("result evidence = %#v", result.Messages)
	}
}

func TestPromptContextValidationBeforeTheModel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		message model.Message
		want    string
	}{
		{name: "assistant role", message: model.Message{Role: model.RoleAssistant, Content: "x"}, want: "is not"},
		{name: "empty", message: model.Message{Role: model.RoleUser}, want: "no content"},
		{name: "tool call", message: model.Message{Role: model.RoleUser, Content: "x", ToolCalls: []model.ToolCall{{Name: "t"}}}, want: "tool-call"},
		{name: "tool result", message: model.Message{Role: model.RoleUser, Content: "x", ToolCallID: "c"}, want: "tool-result"},
		{name: "thinking", message: model.Message{Role: model.RoleUser, Content: "x", Thinking: []model.ThinkingBlock{{Text: "t"}}}, want: "thinking"},
		{name: "bad part", message: model.Message{Role: model.RoleUser, Parts: []model.Part{{Kind: model.PartImage}}}, want: "part 0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeModel{}
			agent, err := golem.New[struct{}, string](client, decoderOf(),
				golem.WithPromptContext[struct{}, string](func(context.Context, golem.RunContext[struct{}]) ([]model.Message, error) {
					return []model.Message{tc.message}, nil
				}),
			)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "hi")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Run() error = %v, want %q", err, tc.want)
			}
			if client.request.Messages != nil {
				t.Fatal("model was called despite invalid prompt context")
			}
		})
	}
}

func TestPromptContextErrorFailsBeforeTheModel(t *testing.T) {
	t.Parallel()

	client := &fakeModel{}
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithPromptContext[struct{}, string](func(context.Context, golem.RunContext[struct{}]) ([]model.Message, error) {
			return nil, errors.New("snapshot unavailable")
		}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "hi")
	if err == nil || !strings.Contains(err.Error(), "snapshot unavailable") {
		t.Fatalf("Run() error = %v", err)
	}
	if client.request.Messages != nil {
		t.Fatal("model was called despite the prompt-context failure")
	}
}

func TestPromptContextRunsOncePerRun(t *testing.T) {
	t.Parallel()

	probe := tool.MustNew(tool.Tool[struct{}]{
		Name:        "probe",
		Description: "probe",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(context.Context, struct{}, json.RawMessage) (tool.Result, error) {
			return tool.Text("ok"), nil
		},
	})
	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "probe", Args: json.RawMessage(`{}`)},
		}}},
		{Message: model.Message{Role: model.RoleAssistant, Content: "done"}},
	}}
	calls := 0
	agent, err := golem.New[struct{}, string](client, decoderOf(),
		golem.WithTools[struct{}, string](probe),
		golem.WithPromptContext[struct{}, string](func(context.Context, golem.RunContext[struct{}]) ([]model.Message, error) {
			calls++
			return []model.Message{{Role: model.RoleUser, Content: "context"}}, nil
		}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "hi"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("prompt context ran %d times, want once per run", calls)
	}
	// The second request carries the same context plus the tool exchange;
	// the context is not re-evaluated.
	if len(client.requests) != 2 {
		t.Fatalf("model requests = %d", len(client.requests))
	}
}
