package session_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/session"
)

// richConversation is a conversation that exercises the durable JSON
// contract: roles, tool calls, parts, and identity stamps.
func richConversation() []model.Message {
	return []model.Message{
		{Role: model.RoleUser, Content: "run it", ConversationID: "conv-1"},
		{Role: model.RoleAssistant, ConversationID: "conv-1", ToolCalls: []model.ToolCall{
			{ID: "c1", Name: "echo", Args: json.RawMessage(`{"n":1}`)},
		}},
		{Role: model.RoleTool, ConversationID: "conv-1", ToolCallID: "c1", ToolName: "echo",
			Content: "1", Parts: []model.Part{model.ImageData("image/png", []byte{1, 2, 3})}},
		{Role: model.RoleAssistant, Content: "done", ConversationID: "conv-1"},
	}
}

func TestMemoryRoundTripsAConversation(t *testing.T) {
	t.Parallel()

	store := session.NewMemory()
	ctx := context.Background()

	messages, err := store.Load(ctx, "conv-1")
	if err != nil {
		t.Fatalf("Load error = %v", err)
	}
	if messages != nil {
		t.Fatalf("empty store Load = %v, want nil", messages)
	}

	if err := store.Save(ctx, "conv-1", richConversation()); err != nil {
		t.Fatalf("Save error = %v", err)
	}
	loaded, err := store.Load(ctx, "conv-1")
	if err != nil {
		t.Fatalf("Load error = %v", err)
	}
	if len(loaded) != 4 {
		t.Fatalf("Load = %d messages, want 4", len(loaded))
	}
	if loaded[1].ToolCalls[0].Name != "echo" {
		t.Fatalf("tool call did not round-trip: %+v", loaded[1])
	}
	if len(loaded[2].Parts) != 1 || loaded[2].Parts[0].Kind != model.PartImage {
		t.Fatalf("parts did not round-trip: %+v", loaded[2])
	}
	if loaded[0].ConversationID != "conv-1" {
		t.Fatalf("identity did not round-trip: %+v", loaded[0])
	}
}

func TestMemorySaveReplacesAndSnapshots(t *testing.T) {
	t.Parallel()

	store := session.NewMemory()
	ctx := context.Background()

	first := richConversation()
	if err := store.Save(ctx, "conv-1", first); err != nil {
		t.Fatalf("Save error = %v", err)
	}

	// Mutating the saved slice afterwards must not touch the store:
	// Save snapshots, it does not alias.
	first[3].Content = "mutated after save"

	replacement := []model.Message{{Role: model.RoleUser, Content: "only", ConversationID: "conv-1"}}
	if err := store.Save(ctx, "conv-1", replacement); err != nil {
		t.Fatalf("Save error = %v", err)
	}
	loaded, err := store.Load(ctx, "conv-1")
	if err != nil {
		t.Fatalf("Load error = %v", err)
	}
	if len(loaded) != 1 || loaded[0].Content != "only" {
		t.Fatalf("Save did not replace: %+v", loaded)
	}

	// Mutating the loaded slice must not corrupt the store either.
	loaded[0].Content = "mutated after load"
	again, err := store.Load(ctx, "conv-1")
	if err != nil {
		t.Fatalf("Load error = %v", err)
	}
	if again[0].Content != "only" {
		t.Fatalf("Load aliased the store: %q", again[0].Content)
	}
}

func TestMemoryConcurrentUse(t *testing.T) {
	t.Parallel()

	store := session.NewMemory()
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("conv-%d", i%2)
			if err := store.Save(ctx, id, []model.Message{
				{Role: model.RoleUser, Content: fmt.Sprintf("turn %d", i), ConversationID: id},
			}); err != nil {
				t.Errorf("Save error = %v", err)
				return
			}
			if _, err := store.Load(ctx, id); err != nil {
				t.Errorf("Load error = %v", err)
			}
		}(i)
	}
	wg.Wait()
}

// failingStore proves the Store interface is implementable — the
// contract tests run the agent over it too.
type failingStore struct {
	loadErr error
	saveErr error
}

func (s *failingStore) Load(context.Context, string) ([]model.Message, error) {
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	return nil, nil
}

func (s *failingStore) Save(context.Context, string, []model.Message) error {
	return s.saveErr
}

var (
	_ session.Store = session.NewMemory()
	_ session.Store = (*failingStore)(nil)
)
