package golem_test

import (
	"context"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
)

func echoDecoder() golem.DecodeFunc[string] {
	return func(_ context.Context, r model.Response) (string, error) {
		return r.Message.Content, nil
	}
}

func TestWithInstructionsFuncEvaluatedPerRunWithDeps(t *testing.T) {
	t.Parallel()

	client := &queuedModel{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, Content: "hi"}},
		{Message: model.Message{Role: model.RoleAssistant, Content: "hi again"}},
	}}
	agent, err := golem.New[playerDeps, string](client, echoDecoder(),
		golem.WithInstructionsFunc[playerDeps, string](
			func(ctx context.Context, runCtx golem.RunContext[playerDeps]) string {
				return "The player's name is " + runCtx.Deps.Name + "."
			}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	first, err := agent.Run(context.Background(), golem.RunContext[playerDeps]{Deps: playerDeps{Name: "Anne"}}, "greet")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := client.requests[0].Messages[0]; got.Role != model.RoleSystem || got.Content != "The player's name is Anne." {
		t.Fatalf("first run system message = %#v", got)
	}

	// The second run re-evaluates against its own dependency value, and
	// the first run's system message in history is replaced, not kept.
	_, err = agent.RunWithHistory(context.Background(),
		golem.RunContext[playerDeps]{Deps: playerDeps{Name: "Ben"}}, first.Messages, "greet again")
	if err != nil {
		t.Fatalf("RunWithHistory() error = %v", err)
	}
	system := 0
	var gotSystem model.Message
	for _, message := range client.requests[1].Messages {
		if message.Role == model.RoleSystem {
			system++
			gotSystem = message
		}
	}
	if system != 1 || gotSystem.Content != "The player's name is Ben." {
		t.Fatalf("second run system messages = %d, last = %#v", system, gotSystem)
	}
}

func TestWithInstructionsFuncJoinsAfterStaticInstructions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		static      string
		dynamic     string
		wantInitial string
	}{
		{name: "joins static and dynamic", static: "Be concise.", dynamic: "Today is Tuesday.", wantInitial: "Be concise.\n\nToday is Tuesday."},
		{name: "empty dynamic keeps static", static: "Be concise.", dynamic: "", wantInitial: "Be concise."},
		{name: "dynamic alone", static: "", dynamic: "Today is Tuesday.", wantInitial: "Today is Tuesday."},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			client := &fakeModel{response: model.Response{
				Message: model.Message{Role: model.RoleAssistant, Content: "ok"},
			}}
			options := []golem.Option[struct{}, string]{
				golem.WithInstructionsFunc[struct{}, string](
					func(context.Context, golem.RunContext[struct{}]) string {
						return test.dynamic
					}),
			}
			if test.static != "" {
				options = append(options, golem.WithInstructions[struct{}, string](test.static))
			}
			agent, err := golem.New[struct{}, string](client, echoDecoder(), options...)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if _, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "hi"); err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if len(client.request.Messages) == 0 {
				t.Fatal("request carried no messages")
			}
			if got := client.request.Messages[0]; got.Role != model.RoleSystem || got.Content != test.wantInitial {
				t.Fatalf("system message = %#v, want %q", got, test.wantInitial)
			}
		})
	}
}

func TestWithInstructionsFuncReceivesRunContext(t *testing.T) {
	t.Parallel()

	type requestIDKey string
	client := &fakeModel{response: model.Response{
		Message: model.Message{Role: model.RoleAssistant, Content: "ok"},
	}}
	agent, err := golem.New[struct{}, string](client, echoDecoder(),
		golem.WithInstructionsFunc[struct{}, string](
			func(ctx context.Context, _ golem.RunContext[struct{}]) string {
				if got := ctx.Value(requestIDKey("request-id")); got != "run-7" {
					t.Errorf("instructions ctx request-id = %v, want run-7", got)
				}
				return "checked"
			}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx := context.WithValue(context.Background(), requestIDKey("request-id"), "run-7")
	if _, err := agent.Run(ctx, golem.RunContext[struct{}]{}, "hi"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}
