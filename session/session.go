// Package session persists conversations between runs through a small
// port. Golem owns when to load and save — the WithSession run option
// loads the stored history before a run and saves the conversation
// after it, partial evidence included when a run fails — and a Store
// implementation owns where. The module ships one offline
// implementation, Memory; file, database, and cache backends stay
// application-side behind the same interface.
//
// The port follows the module's rule for optional capability: an
// interface the application implements, never a dependency it must
// take. See docs/adr/0039-sessions.md.
package session

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/abubakarsiddik31/golem/model"
)

// Store persists one conversation's messages under its conversation
// ID. Load and Save are value semantics: Save replaces the stored
// conversation wholesale, and Load returns what Save stored —
// implementations must snapshot, because callers keep mutating their
// message slices after saving.
//
// A Store is safe for concurrent use when its implementation is;
// concurrent runs that continue the same conversation race at the
// application layer no matter the backend — the second save overwrites
// the first — so runs of one conversation belong to one goroutine, or
// to a Store that serializes them.
type Store interface {
	// Load returns the conversation's stored messages, or nil with no
	// error when the conversation has none. Messages decode from the
	// durable model.Message JSON contract.
	Load(ctx context.Context, conversationID string) ([]model.Message, error)
	// Save replaces the conversation's stored messages. The messages
	// are the run's canonical conversation — Result.Messages after
	// success, RunError.Partial.Messages after a failure — stamped with
	// the run's identity.
	Save(ctx context.Context, conversationID string, messages []model.Message) error
}

// Memory is an in-process Store: a map from conversation ID to a
// snapshot of the conversation, taken by encoding to the durable
// message JSON, so a stored conversation shares nothing with the
// caller that saved it. It keeps conversations until discarded and
// persists nothing across processes.
type Memory struct {
	mu    sync.Mutex
	store map[string][]byte
}

// NewMemory returns an empty Memory store.
func NewMemory() *Memory {
	return &Memory{store: map[string][]byte{}}
}

// Load returns the conversation's stored messages, or nil when the
// conversation has none.
func (m *Memory) Load(_ context.Context, conversationID string) ([]model.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	encoded, ok := m.store[conversationID]
	if !ok {
		return nil, nil
	}
	var messages []model.Message
	if err := json.Unmarshal(encoded, &messages); err != nil {
		return nil, fmt.Errorf("session: decode stored conversation: %w", err)
	}
	return messages, nil
}

// Save replaces the conversation's stored messages with a snapshot.
func (m *Memory) Save(_ context.Context, conversationID string, messages []model.Message) error {
	encoded, err := json.Marshal(messages)
	if err != nil {
		return fmt.Errorf("session: encode conversation: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.store == nil {
		m.store = map[string][]byte{}
	}
	m.store[conversationID] = encoded
	return nil
}
