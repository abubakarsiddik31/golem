package golem_test

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
)

// answerModel returns one text answer per Generate call.
func answerModel(replies ...string) *queuedModel {
	responses := make([]model.Response, len(replies))
	for i, reply := range replies {
		responses[i] = model.Response{
			Message: model.Message{Role: model.RoleAssistant, Content: reply},
			Usage:   model.Usage{InputTokens: 3, OutputTokens: 2},
		}
	}
	return &queuedModel{responses: responses}
}

func identityAgent(t *testing.T, m *queuedModel) *golem.Agent[struct{}, string] {
	t.Helper()
	agent, err := golem.New[struct{}, string](m, decoderOf())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return agent
}

// uuidShape matches a canonical UUID string; its capture groups expose
// the version and variant nibbles the RFC pins.
var uuidShape = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-([0-9a-f]{4})-([0-9a-f]{4})-[0-9a-f]{12}$`)

func TestNewIDIsUUIDVersion7(t *testing.T) {
	t.Parallel()

	seen := make(map[string]struct{}, 64)
	for i := 0; i < 64; i++ {
		id := golem.NewID()
		groups := uuidShape.FindStringSubmatch(id)
		if groups == nil {
			t.Fatalf("NewID() = %q, want canonical UUID form", id)
		}
		if !strings.HasPrefix(groups[1], "7") {
			t.Fatalf("NewID() = %q, want version 7", id)
		}
		switch groups[2][0] {
		case '8', '9', 'a', 'b':
		default:
			t.Fatalf("NewID() = %q, want an RFC 4122 variant", id)
		}
		if _, dupe := seen[id]; dupe {
			t.Fatalf("NewID() repeated %q across mints", id)
		}
		seen[id] = struct{}{}
	}
}

func TestRunMintsDistinctRunAndConversationIDs(t *testing.T) {
	t.Parallel()

	agent := identityAgent(t, answerModel("first", "second"))

	first, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "hi")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	second, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "hi again")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if first.RunID == "" || first.RunID == second.RunID {
		t.Fatalf("run IDs = %q and %q, want distinct non-empty mints", first.RunID, second.RunID)
	}
	// Fresh history per run: each starts its own conversation.
	if first.ConversationID == "" || first.ConversationID == second.ConversationID {
		t.Fatalf("conversation IDs = %q and %q, want distinct fresh conversations", first.ConversationID, second.ConversationID)
	}
}

func TestRunWithHistoryInheritsConversationID(t *testing.T) {
	t.Parallel()

	agent := identityAgent(t, answerModel("first", "second"))

	first, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "hi")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	second, err := agent.RunWithHistory(context.Background(), golem.RunContext[struct{}]{},
		first.Messages, "and then?")
	if err != nil {
		t.Fatalf("RunWithHistory() error = %v", err)
	}

	if second.ConversationID != first.ConversationID {
		t.Fatalf("continued conversation = %q, want the history's %q", second.ConversationID, first.ConversationID)
	}
	if second.RunID == first.RunID {
		t.Fatalf("continued run ID = %q, want a fresh mint", second.RunID)
	}
}

func TestRunIdentityOverridesAndForks(t *testing.T) {
	t.Parallel()

	agent := identityAgent(t, answerModel("first", "second"))

	first, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "hi",
		golem.WithRunID("trace-1"), golem.WithConversationID("conv-1"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if first.RunID != "trace-1" || first.ConversationID != "conv-1" {
		t.Fatalf("identity = %q/%q, want the supplied trace-1/conv-1", first.RunID, first.ConversationID)
	}

	// Forking: the history carries conv-1, but the explicit identifier wins.
	fork, err := agent.RunWithHistory(context.Background(), golem.RunContext[struct{}]{},
		first.Messages, "fork this", golem.WithConversationID("conv-2"))
	if err != nil {
		t.Fatalf("RunWithHistory() error = %v", err)
	}
	if fork.ConversationID != "conv-2" {
		t.Fatalf("forked conversation = %q, want the explicit conv-2", fork.ConversationID)
	}
	if fork.RunID == "trace-1" {
		t.Fatal("forked run inherited the previous run's ID; run IDs never carry over")
	}
}

func TestRunIdentityStampsEventsAndMessages(t *testing.T) {
	t.Parallel()

	agent := identityAgent(t, answerModel("first", "second"))

	first, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "hi",
		golem.WithRunObserver(func(golem.RunEvent) {}))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// Continue under observation: every event of the run carries the run's
	// identity, and only the messages the run added carry it.
	var events []golem.RunEvent
	second, err := agent.RunWithHistory(context.Background(), golem.RunContext[struct{}]{},
		first.Messages, "and then?", golem.WithRunObserver(func(event golem.RunEvent) {
			events = append(events, event)
		}))
	if err != nil {
		t.Fatalf("RunWithHistory() error = %v", err)
	}
	if len(events) == 0 {
		t.Fatal("no events observed, want at least one model attempt")
	}
	for _, event := range events {
		if event.RunID != second.RunID || event.ConversationID != second.ConversationID {
			t.Fatalf("event %+v carries %q/%q, want %q/%q",
				event.Kind, event.RunID, event.ConversationID, second.RunID, second.ConversationID)
		}
	}

	supplied := len(first.Messages)
	if len(second.Messages) <= supplied {
		t.Fatalf("continued history = %d messages, want the supplied %d plus this run's turn", len(second.Messages), supplied)
	}
	for _, message := range second.Messages[:supplied] {
		// Supplied history keeps the identity it carried in — run one's
		// stamps, which is exactly what conversation inheritance reads.
		if message.RunID != first.RunID || message.ConversationID != first.ConversationID {
			t.Fatalf("supplied message %q carries %q/%q, want its own run's %q/%q",
				message.Role, message.RunID, message.ConversationID, first.RunID, first.ConversationID)
		}
	}
	for _, message := range second.Messages[supplied:] {
		if message.RunID != second.RunID || message.ConversationID != second.ConversationID {
			t.Fatalf("run-added message %q carries %q/%q, want the run's %q/%q",
				message.Role, message.RunID, message.ConversationID, second.RunID, second.ConversationID)
		}
	}
	// The caller's own slice stays untouched.
	for _, message := range first.Messages {
		if message.RunID != first.RunID || message.ConversationID != first.ConversationID {
			t.Fatalf("first run's message identity changed: %q/%q", message.RunID, message.ConversationID)
		}
	}
}

func TestStreamCarriesRunIdentity(t *testing.T) {
	t.Parallel()

	var events []golem.RunEvent
	agent, err := golem.New[struct{}, string](streamClient(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "streamed"}},
	), decoderOf(), golem.WithRunEvents[struct{}, string](func(event golem.RunEvent) {
		events = append(events, event)
	}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := agent.RunStream(context.Background(), golem.RunContext[struct{}]{}, "hi",
		func(model.Delta) error { return nil })
	if err != nil {
		t.Fatalf("RunStream() error = %v", err)
	}
	if len(events) == 0 {
		t.Fatal("no events observed on the streamed run")
	}
	for _, event := range events {
		if event.RunID != result.RunID || event.ConversationID != result.ConversationID {
			t.Fatalf("streamed event carries %q/%q, want the result's %q/%q",
				event.RunID, event.ConversationID, result.RunID, result.ConversationID)
		}
	}
	for _, message := range result.Messages {
		if message.RunID != result.RunID {
			t.Fatalf("streamed message carries run %q, want %q", message.RunID, result.RunID)
		}
	}
}

func TestFailedRunCarriesIdentityInPartial(t *testing.T) {
	t.Parallel()

	client := answerModel("first")
	agent := identityAgent(t, client)

	first, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "hi")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// The queued model is exhausted: the second run fails with partial
	// evidence — an assistant turn exists — and must still identify itself.
	_, err = agent.RunWithHistory(context.Background(), golem.RunContext[struct{}]{},
		first.Messages, "and then?")
	var runErr *golem.RunError
	if err == nil {
		t.Fatal("second run succeeded; want the exhausted-queue failure")
	}
	if !errors.As(err, &runErr) || runErr.Partial == nil {
		t.Fatalf("error = %v, want a RunError with partial evidence", err)
	}
	if runErr.Partial.RunID == "" || runErr.Partial.RunID == first.RunID {
		t.Fatalf("partial RunID = %q, want a fresh mint distinct from %q", runErr.Partial.RunID, first.RunID)
	}
	if runErr.Partial.ConversationID != first.ConversationID {
		t.Fatalf("partial conversation = %q, want the inherited %q", runErr.Partial.ConversationID, first.ConversationID)
	}
}

func TestDeferredResumeContinuesConversation(t *testing.T) {
	t.Parallel()

	_, agent, _ := gateClient(t,
		pauseResponse("delete_file"),
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "deleted"}, Usage: model.Usage{InputTokens: 4, OutputTokens: 2}},
	)

	paused, err := agent.Run(context.Background(), golem.RunContext[gateDeps]{}, "delete notes.txt")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if paused.Pending == nil {
		t.Fatalf("Run() = %#v, want a paused run", paused)
	}

	resumed, err := agent.RunWithDeferredResults(context.Background(), golem.RunContext[gateDeps]{},
		paused.Messages, golem.DeferredResults{Approvals: map[string]golem.Approval{
			"call-1": {Approved: true},
		}}, "")
	if err != nil {
		t.Fatalf("RunWithDeferredResults() error = %v", err)
	}
	if resumed.ConversationID != paused.ConversationID {
		t.Fatalf("resumed conversation = %q, want the paused run's %q", resumed.ConversationID, paused.ConversationID)
	}
	if resumed.RunID == paused.RunID {
		t.Fatalf("resumed run ID = %q, want a fresh mint", resumed.RunID)
	}
	// The approval resolution is the resuming run's addition.
	for _, message := range resumed.Messages {
		if message.Role == model.RoleTool && message.RunID != resumed.RunID {
			t.Fatalf("resolution message carries run %q, want the resuming run's %q", message.RunID, resumed.RunID)
		}
	}
}
