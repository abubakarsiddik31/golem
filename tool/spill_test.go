package tool_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem/tool"
)

func TestMemorySpillRoundTrip(t *testing.T) {
	t.Parallel()

	var store tool.MemorySpill
	ctx := context.Background()
	handle, err := store.Put(ctx, "0123456789")
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	text, total, err := store.Read(ctx, handle, 2, 3)
	if err != nil || text != "234" || total != 10 {
		t.Fatalf("Read() = %q, %d, %v", text, total, err)
	}
	text, total, err = store.Read(ctx, handle, -1, 100)
	if err != nil || text != "0123456789" || total != 10 {
		t.Fatalf("Read(negative) = %q, %d, %v", text, total, err)
	}
	text, _, err = store.Read(ctx, handle, 8, 0)
	if err != nil || text != "89" {
		t.Fatalf("Read(tail) = %q, %v", text, err)
	}
	if _, _, err := store.Read(ctx, "missing", 0, 1); !errors.Is(err, tool.ErrSpillNotFound) {
		t.Fatalf("missing handle error = %v", err)
	}
}

func TestMemorySpillClampsReadLimit(t *testing.T) {
	t.Parallel()

	store := tool.NewMemorySpill()
	handle, err := store.Put(context.Background(), strings.Repeat("x", tool.MaxSpillReadLimit+100))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	text, total, err := store.Read(context.Background(), handle, 0, tool.MaxSpillReadLimit+1000)
	if err != nil || len(text) != tool.MaxSpillReadLimit || total != tool.MaxSpillReadLimit+100 {
		t.Fatalf("Read() bytes=%d total=%d err=%v", len(text), total, err)
	}
}
