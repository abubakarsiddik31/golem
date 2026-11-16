package memory_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem/memory"
	"github.com/abubakarsiddik31/golem/model"
)

func TestSnapshotRendersBoundedNotebook(t *testing.T) {
	t.Parallel()

	type deps struct{ Tenant string }
	store := memory.NewMemory()
	cfg := memory.Config[deps]{
		Store:     store,
		Namespace: func(d deps) string { return d.Tenant },
	}
	tools := newTools(t, cfg)
	exec(t, tools, "write_memory", deps{Tenant: "alice"}, `{"path":"MEMORY.md","content":"Prefers concise answers."}`)
	exec(t, tools, "write_memory", deps{Tenant: "alice"}, `{"path":"notes/auth.md","content":"Uses OAuth."}`)

	message, ok, err := memory.Snapshot(context.Background(), cfg, deps{Tenant: "alice"})
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if !ok || message.Role != model.RoleUser {
		t.Fatalf("Snapshot() = %#v, %v", message, ok)
	}
	for _, want := range []string{"Stored memory from earlier runs", "Prefers concise answers.", "notes/auth.md"} {
		if !strings.Contains(message.Content, want) {
			t.Fatalf("snapshot missing %q: %q", want, message.Content)
		}
	}
	if strings.Contains(message.Content, "alice") {
		t.Fatalf("snapshot leaked the namespace: %q", message.Content)
	}
}

func TestSnapshotEmptyNotebook(t *testing.T) {
	t.Parallel()

	cfg := memory.Config[struct{}]{Store: memory.NewMemory()}
	message, ok, err := memory.Snapshot(context.Background(), cfg, struct{}{})
	if err != nil || ok || message.Content != "" {
		t.Fatalf("Snapshot() = %#v, %v, %v; want nothing", message, ok, err)
	}
}

func TestSnapshotBounds(t *testing.T) {
	t.Parallel()

	store := memory.NewMemory()
	cfg := memory.Config[struct{}]{
		Store:            store,
		MaxSnapshotChars: 220,
		MaxSnapshotLines: 2,
	}
	if err := store.Write(context.Background(), "", memory.MainFile,
		"line one\nline two\nline three\n"+strings.Repeat("detail ", 100)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	message, ok, err := memory.Snapshot(context.Background(), cfg, struct{}{})
	if err != nil || !ok {
		t.Fatalf("Snapshot() = %#v, %v, %v", message, ok, err)
	}
	if len(message.Content) > 220 {
		t.Fatalf("snapshot is %d bytes, over the 220-byte bound", len(message.Content))
	}
	if !strings.Contains(message.Content, "truncated") {
		t.Fatalf("bounded snapshot lacks a truncation note: %q", message.Content)
	}
	if strings.Contains(message.Content, "line three") {
		t.Fatalf("line bound not applied: %q", message.Content)
	}
}

func TestSnapshotNamespaceIsolation(t *testing.T) {
	t.Parallel()

	type deps struct{ Tenant string }
	store := memory.NewMemory()
	cfg := memory.Config[deps]{
		Store:     store,
		Namespace: func(d deps) string { return d.Tenant },
	}
	if err := store.Write(context.Background(), "alice", "MEMORY.md", "alice note"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, ok, err := memory.Snapshot(context.Background(), cfg, deps{Tenant: "bob"}); err != nil || ok {
		t.Fatalf("bob snapshot = ok %v, err %v; want empty", ok, err)
	}
	message, ok, err := memory.Snapshot(context.Background(), cfg, deps{Tenant: "alice"})
	if err != nil || !ok || !strings.Contains(message.Content, "alice note") {
		t.Fatalf("alice snapshot = %#v, %v, %v", message, ok, err)
	}
}

func TestSnapshotFailures(t *testing.T) {
	t.Parallel()

	if _, _, err := memory.Snapshot(context.Background(), memory.Config[struct{}]{Store: failingStore{}}, struct{}{}); err == nil {
		t.Fatal("store failure was swallowed")
	}
	type deps struct{ Tenant string }
	cfg := memory.Config[deps]{
		Store:            memory.NewMemory(),
		Namespace:        func(d deps) string { return d.Tenant },
		MaxSnapshotChars: -1,
	}
	_, _, err := memory.Snapshot(context.Background(), cfg, deps{Tenant: "alice"})
	if err == nil || !strings.Contains(err.Error(), "MaxSnapshotChars") {
		t.Fatalf("negative snapshot budget error = %v", err)
	}
	cfg.MaxSnapshotChars = 0
	_, _, err = memory.Snapshot(context.Background(), cfg, deps{Tenant: "../bob"})
	if err == nil || !strings.Contains(err.Error(), "namespace") {
		t.Fatalf("invalid namespace error = %v", err)
	}
}

func TestSnapshotReadFailure(t *testing.T) {
	t.Parallel()

	_, _, err := memory.Snapshot(context.Background(), memory.Config[struct{}]{Store: readFailStore{}}, struct{}{})
	if err == nil || !strings.Contains(err.Error(), "read failed") {
		t.Fatalf("read failure error = %v", err)
	}
}

type readFailStore struct{}

func (readFailStore) List(context.Context, string) ([]string, error) {
	return []string{memory.MainFile}, nil
}

func (readFailStore) Read(context.Context, string, string) (memory.File, error) {
	return memory.File{}, errors.New("read failed")
}

func (readFailStore) Write(context.Context, string, string, string) error { return nil }
func (readFailStore) Delete(context.Context, string, string) error        { return nil }
