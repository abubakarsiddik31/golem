package golem_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/session"
	"github.com/abubakarsiddik31/golem/testmodel"
	"github.com/abubakarsiddik31/golem/tool"
)

// sessionStore is a Store whose failures tests control.
type sessionStore struct {
	stored  map[string][]model.Message
	loadErr error
	saveErr error
}

func newSessionStore() *sessionStore {
	return &sessionStore{stored: map[string][]model.Message{}}
}

func (s *sessionStore) Load(_ context.Context, conversationID string) ([]model.Message, error) {
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	return s.stored[conversationID], nil
}

func (s *sessionStore) Save(_ context.Context, conversationID string, messages []model.Message) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.stored[conversationID] = messages
	return nil
}

func TestSessionChainsRunsThroughTheStore(t *testing.T) {
	t.Parallel()

	m := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "first answer"},
			Usage: model.Usage{InputTokens: 3, OutputTokens: 1}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "second answer"},
			Usage: model.Usage{InputTokens: 9, OutputTokens: 2}},
	)
	agent, err := golem.New[struct{}, string](m, golem.DecodeFunc[string](decodeContent))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	store := newSessionStore()

	first, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "who wins?",
		golem.WithSession(store), golem.WithConversationID("conv-1"))
	if err != nil {
		t.Fatalf("first Run error = %v", err)
	}

	// The second run passes no history: the store supplies it.
	second, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "and now?",
		golem.WithSession(store), golem.WithConversationID("conv-1"))
	if err != nil {
		t.Fatalf("second Run error = %v", err)
	}
	if second.Output != "second answer" {
		t.Fatalf("second output = %q", second.Output)
	}

	requests := m.Requests()
	if len(requests) != 2 {
		t.Fatalf("model saw %d requests, want 2", len(requests))
	}
	// The second request carries the first turn's messages before the
	// fresh prompt, exactly as a hand-passed history would.
	var carried bool
	for _, message := range requests[1].Messages {
		if message.Role == model.RoleUser && message.Content == "who wins?" {
			carried = true
		}
	}
	if !carried {
		t.Fatalf("second request lost the first turn: %+v", requests[1].Messages)
	}
	if second.ConversationID != "conv-1" || first.ConversationID != "conv-1" {
		t.Fatalf("conversation ids = %q, %q", first.ConversationID, second.ConversationID)
	}
	if len(store.stored["conv-1"]) != 4 {
		t.Fatalf("store holds %d messages, want the full four-message conversation",
			len(store.stored["conv-1"]))
	}
}

func TestSessionRoundTripsResultConversationID(t *testing.T) {
	t.Parallel()

	m := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "one"}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "two"}},
	)
	agent, err := golem.New[struct{}, string](m, golem.DecodeFunc[string](decodeContent))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	store := session.NewMemory()

	// No pinned identifier: the run mints one, saves under it, and
	// reports it, so the caller can continue the conversation without
	// ever holding the messages.
	first, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go",
		golem.WithSession(store))
	if err != nil {
		t.Fatalf("first Run error = %v", err)
	}
	if first.ConversationID == "" {
		t.Fatal("first run minted no conversation id")
	}
	second, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "again",
		golem.WithSession(store), golem.WithConversationID(first.ConversationID))
	if err != nil {
		t.Fatalf("second Run error = %v", err)
	}
	if second.ConversationID != first.ConversationID {
		t.Fatalf("conversation ids = %q then %q", first.ConversationID, second.ConversationID)
	}
	if len(m.Requests()[1].Messages) < 3 {
		t.Fatalf("second request has %d messages, want the loaded conversation plus the prompt",
			len(m.Requests()[1].Messages))
	}
}

func TestSessionLoadFailureFailsBeforeAnyModelCall(t *testing.T) {
	t.Parallel()

	m := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "never"}},
	)
	agent, err := golem.New[struct{}, string](m, golem.DecodeFunc[string](decodeContent))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	store := &sessionStore{loadErr: errors.New("db closed")}

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go",
		golem.WithSession(store), golem.WithConversationID("conv-1"))
	if err == nil || !strings.Contains(err.Error(), "load session") || !strings.Contains(err.Error(), "db closed") {
		t.Fatalf("Run error = %v, want the wrapped load failure", err)
	}
	if len(m.Requests()) != 0 {
		t.Fatal("the model was called despite a session load failure")
	}
}

func TestSessionSaveFailureReturnsTheResultAndTheError(t *testing.T) {
	t.Parallel()

	m := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "the answer"}},
	)
	agent, err := golem.New[struct{}, string](m, golem.DecodeFunc[string](decodeContent))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	store := &sessionStore{saveErr: errors.New("disk full")}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go",
		golem.WithSession(store), golem.WithConversationID("conv-1"))
	// The model answered; only the persistence failed. The result comes
	// back alongside the error, so the answer is not lost.
	if err == nil || !strings.Contains(err.Error(), "save session") || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("Run error = %v, want the wrapped save failure", err)
	}
	if result.Output != "the answer" {
		t.Fatalf("output = %q, want the model's answer despite the save failure", result.Output)
	}
	if len(result.Messages) != 2 {
		t.Fatalf("messages = %d, want the conversation", len(result.Messages))
	}
}

func TestSessionSavesPartialEvidenceAndSkipsActivitylessFailures(t *testing.T) {
	t.Parallel()

	// A decoder that always rejects: the model answers, the run fails at
	// the decode stage with partial evidence.
	rejecting := golem.DecodeFunc[int](func(_ context.Context, r model.Response) (int, error) {
		return 0, &model.ModelRetry{Err: errors.New("not a number")}
	})
	m := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "forty two"},
			Usage: model.Usage{InputTokens: 4, OutputTokens: 1}},
	)
	agent, err := golem.New[struct{}, int](m, rejecting)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	store := newSessionStore()

	_, err = agent.Run(context.Background(), golem.RunContext[struct{}]{}, "how many?",
		golem.WithSession(store), golem.WithConversationID("conv-1"))
	if err == nil {
		t.Fatal("run succeeded despite the rejecting decoder")
	}
	// The failed run's transcript is in the store, so a later run can
	// resume from it.
	saved := store.stored["conv-1"]
	if len(saved) != 2 {
		t.Fatalf("store holds %d messages after the failed run, want the partial transcript",
			len(saved))
	}

	// A failure before any activity saves nothing: a fresh conversation
	// under another id stays absent, and an existing one is not clobbered.
	failing := testmodel.New().Fail(errors.New("provider down"))
	failingAgent, err := golem.New[struct{}, string](failing, golem.DecodeFunc[string](decodeContent))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = failingAgent.Run(context.Background(), golem.RunContext[struct{}]{}, "go",
		golem.WithSession(store), golem.WithConversationID("conv-2"))
	if err == nil {
		t.Fatal("run succeeded despite the failing model")
	}
	if _, ok := store.stored["conv-2"]; ok {
		t.Fatal("an activityless failure was saved")
	}
	if len(store.stored["conv-1"]) != 2 {
		t.Fatal("the earlier conversation was clobbered by the failed run")
	}
}

func TestSessionExplicitHistoryBypassesLoadAndSavesBack(t *testing.T) {
	t.Parallel()

	m := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "seeded"}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "continued"}},
	)
	agent, err := golem.New[struct{}, string](m, golem.DecodeFunc[string](decodeContent))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	store := newSessionStore()

	// Seed a stored conversation under conv-1.
	if _, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "seed",
		golem.WithSession(store), golem.WithConversationID("conv-1")); err != nil {
		t.Fatalf("seeding run error = %v", err)
	}

	// A run with explicit history does not load conv-1: the caller's
	// history is the conversation, under its own inherited identity.
	history := []model.Message{
		{Role: model.RoleUser, Content: "imported question", ConversationID: "imported"},
	}
	result, err := agent.RunWithHistory(context.Background(), golem.RunContext[struct{}]{},
		history, "continue", golem.WithSession(store))
	if err != nil {
		t.Fatalf("RunWithHistory error = %v", err)
	}
	if result.ConversationID != "imported" {
		t.Fatalf("conversation id = %q, want the history's inherited %q", result.ConversationID, "imported")
	}
	if len(store.stored["imported"]) != 3 {
		t.Fatalf("history run saved %d messages, want the history, the prompt, and the answer", len(store.stored["imported"]))
	}
	// The conv-1 store entry is untouched by the explicit-history run.
	if len(store.stored["conv-1"]) != 2 {
		t.Fatal("the seeded conversation was modified")
	}
}

func TestSessionResumeSavesTheContinuedConversation(t *testing.T) {
	t.Parallel()

	gated := tool.MustNew(tool.Tool[struct{}]{
		Name: "gated", Description: "Needs sign-off.", Schema: json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, _ struct{}, _ json.RawMessage) (tool.Result, error) {
			if tool.CallApproved(ctx) {
				return tool.Text("signed off"), nil
			}
			return tool.Result{}, &tool.Deferred{Kind: tool.DeferApproval, Reason: "sign-off"}
		},
	})
	pausing := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "c1", Name: "gated", Args: json.RawMessage(`{}`)},
		}}},
	)
	agent, err := golem.New[struct{}, string](pausing, golem.DecodeFunc[string](decodeContent),
		golem.WithTools[struct{}, string](gated))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	store := newSessionStore()

	paused, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "go",
		golem.WithSession(store), golem.WithConversationID("conv-1"))
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if paused.Pending == nil {
		t.Fatal("run did not pause")
	}
	// A pause is a success-path end: its transcript is saved, so a
	// process that dies here can still resume from the store.
	if len(store.stored["conv-1"]) == 0 {
		t.Fatal("the paused transcript was not saved")
	}

	resumedModel := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "approved and done"}},
	)
	resumer, err := golem.New[struct{}, string](resumedModel, golem.DecodeFunc[string](decodeContent),
		golem.WithTools[struct{}, string](gated))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := resumer.RunWithDeferredResults(context.Background(), golem.RunContext[struct{}]{},
		paused.Messages, golem.DeferredResults{Approvals: map[string]golem.Approval{
			"c1": {Approved: true},
		}}, "", golem.WithSession(store), golem.WithConversationID("conv-1"))
	if err != nil {
		t.Fatalf("RunWithDeferredResults error = %v", err)
	}
	if result.Output != "approved and done" {
		t.Fatalf("output = %q", result.Output)
	}
	if len(store.stored["conv-1"]) != 4 {
		t.Fatalf("store holds %d messages after the resume, want the continued conversation",
			len(store.stored["conv-1"]))
	}
}

func TestSessionHistoryProcessorAppliesToLoadedHistory(t *testing.T) {
	t.Parallel()

	m := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "one"}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "two"}},
	)
	agent, err := golem.New[struct{}, string](m, golem.DecodeFunc[string](decodeContent),
		golem.WithHistoryProcessor[struct{}, string](golem.TrimHistory(1)))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	store := newSessionStore()

	if _, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "first",
		golem.WithSession(store), golem.WithConversationID("conv-1")); err != nil {
		t.Fatalf("first Run error = %v", err)
	}
	if _, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "second",
		golem.WithSession(store), golem.WithConversationID("conv-1")); err != nil {
		t.Fatalf("second Run error = %v", err)
	}
	// The processor bounded the loaded history before the request: the
	// model saw the last stored message plus the prompt, not the whole
	// conversation.
	requests := m.Requests()
	for _, message := range requests[1].Messages {
		if message.Role == model.RoleUser && message.Content == "first" {
			t.Fatalf("loaded history was not trimmed: %+v", requests[1].Messages)
		}
	}
}

// Fails to compile if WithSession stops accepting the Store interface.
var _ = func() {
	var opts golem.RunOption = golem.WithSession(session.NewMemory())
	_ = opts
}
