package tool

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrSpillNotFound reports a handle a SpillStore does not hold. The
// spill-read tool turns it into a failure the model can correct.
var ErrSpillNotFound = errors.New("tool: spill not found")

const (
	// DefaultSpillReadLimit bounds one spill read when the caller does
	// not set a limit.
	DefaultSpillReadLimit = 16 << 10
	// MaxSpillReadLimit bounds one spill read regardless of the
	// requested limit.
	MaxSpillReadLimit = 64 << 10
)

// SpillStore persists a complete tool result under a handle so the model
// can page it back after the result was replaced with a marker. Put may
// be called concurrently; Read returns the requested byte slice and the
// stored text's total size. A missing handle returns ErrSpillNotFound.
// Backends stay application-side; MemorySpill is the offline
// implementation.
type SpillStore interface {
	Put(ctx context.Context, text string) (handle string, err error)
	Read(ctx context.Context, handle string, offset, limit int) (text string, total int, err error)
}

// MemorySpill is an in-process SpillStore. It is safe for concurrent use
// and keeps spilled text until discarded.
type MemorySpill struct {
	mu      sync.Mutex
	next    int
	entries map[string]string
}

// NewMemorySpill returns an empty in-process spill store.
func NewMemorySpill() *MemorySpill {
	return &MemorySpill{entries: make(map[string]string)}
}

// Put stores text and returns a process-local handle.
func (m *MemorySpill) Put(_ context.Context, text string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.entries == nil {
		m.entries = make(map[string]string)
	}
	m.next++
	handle := fmt.Sprintf("spill-%d", m.next)
	m.entries[handle] = text
	return handle, nil
}

// Read returns the requested slice and the total size. Offset and limit
// are clamped to the stored text and MaxSpillReadLimit; a negative offset
// starts from zero. Slice boundaries are not adjusted for UTF-8 because
// spills are arbitrary payloads, not necessarily text.
func (m *MemorySpill) Read(_ context.Context, handle string, offset, limit int) (string, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	text, ok := m.entries[handle]
	if !ok {
		return "", 0, ErrSpillNotFound
	}
	total := len(text)
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	if limit <= 0 {
		limit = DefaultSpillReadLimit
	}
	if limit > MaxSpillReadLimit {
		limit = MaxSpillReadLimit
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return text[offset:end], total, nil
}
