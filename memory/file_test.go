package memory_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem/memory"
	"github.com/abubakarsiddik31/golem/tool"
)

func TestFileStoreToolsPersistAcrossInstances(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := memory.NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	tools := newTools(t, memory.Config[struct{}]{Store: store})
	exec(t, tools, "write_memory", struct{}{}, `{"path":"MEMORY.md","content":"durable note"}`)
	exec(t, tools, "write_memory", struct{}{}, `{"path":"notes/auth.md","content":"oauth"}`)

	reopened, err := memory.NewFileStore(root)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	reopenedTools := newTools(t, memory.Config[struct{}]{Store: reopened})
	if got := exec(t, reopenedTools, "read_memory", struct{}{}, `{"path":"MEMORY.md"}`); got != "durable note" {
		t.Fatalf("persisted content = %q", got)
	}
	list := exec(t, reopenedTools, "list_memory", struct{}{}, `{}`)
	if !strings.Contains(list, "MEMORY.md") || !strings.Contains(list, "notes/auth.md") {
		t.Fatalf("list = %q", list)
	}
	if got := exec(t, reopenedTools, "delete_memory", struct{}{}, `{"path":"notes/auth.md"}`); !strings.Contains(got, "Deleted") {
		t.Fatalf("delete = %q", got)
	}
}

func TestFileStoreCompareAndSwap(t *testing.T) {
	t.Parallel()

	store, err := memory.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	ctx := context.Background()
	if err := store.WriteVersioned(ctx, "", "MEMORY.md", "one", ""); err != nil {
		t.Fatalf("create: %v", err)
	}
	file, err := store.Read(ctx, "", "MEMORY.md")
	if err != nil || file.Version == "" {
		t.Fatalf("Read() = %#v, %v", file, err)
	}
	if err := store.WriteVersioned(ctx, "", "MEMORY.md", "two", file.Version); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := store.WriteVersioned(ctx, "", "MEMORY.md", "stale", file.Version); !errors.Is(err, memory.ErrVersionConflict) {
		t.Fatalf("stale write error = %v", err)
	}
	if err := store.DeleteVersioned(ctx, "", "MEMORY.md", file.Version); !errors.Is(err, memory.ErrVersionConflict) {
		t.Fatalf("stale delete error = %v", err)
	}
	if err := store.WriteVersioned(ctx, "", "MEMORY.md", "create", ""); !errors.Is(err, memory.ErrVersionConflict) {
		t.Fatalf("create over existing error = %v", err)
	}
}

func TestMemoryCompareAndSwap(t *testing.T) {
	t.Parallel()

	store := memory.NewMemory()
	ctx := context.Background()
	if err := store.WriteVersioned(ctx, "", "MEMORY.md", "one", ""); err != nil {
		t.Fatalf("create: %v", err)
	}
	file, _ := store.Read(ctx, "", "MEMORY.md")
	if err := store.WriteVersioned(ctx, "", "MEMORY.md", "two", file.Version); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := store.WriteVersioned(ctx, "", "MEMORY.md", "stale", file.Version); !errors.Is(err, memory.ErrVersionConflict) {
		t.Fatalf("stale write error = %v", err)
	}
}

func TestFileStoreRejectsSymlinkEscapes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.md"), []byte("secret"), 0o600); err != nil {
		t.Fatalf("seed outside: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatalf("symlink dir: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(root, "secret-link.md")); err != nil {
		t.Fatalf("symlink file: %v", err)
	}
	store, err := memory.NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	ctx := context.Background()
	if _, err := store.Read(ctx, "", "link/secret.md"); err == nil {
		t.Fatal("read through escaping symlink dir allowed")
	}
	if _, err := store.Read(ctx, "", "secret-link.md"); err == nil {
		t.Fatal("read through escaping symlink file allowed")
	}
	if err := store.Write(ctx, "", "link/new.md", "x"); err == nil {
		t.Fatal("write through escaping symlink dir allowed")
	}
	paths, err := store.List(ctx, "")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(paths) != 0 {
		t.Fatalf("List() = %v, want symlinks skipped", paths)
	}
}

func TestToolReportsVersionConflictAsCorrectable(t *testing.T) {
	t.Parallel()

	tools := newTools(t, memory.Config[struct{}]{Store: conflictingStore{Memory: memory.NewMemory()}})
	_, err := execErr(tools, "write_memory", struct{}{}, `{"path":"MEMORY.md","content":"x"}`)
	var failed *tool.Failed
	if !errors.As(err, &failed) || !strings.Contains(failed.Reason, "changed concurrently") {
		t.Fatalf("write error = %v", err)
	}
}

type conflictingStore struct{ *memory.Memory }

func (conflictingStore) Read(context.Context, string, string) (memory.File, error) {
	return memory.File{Path: "MEMORY.md", Content: "old", Version: "v1"}, nil
}

func (conflictingStore) WriteVersioned(context.Context, string, string, string, string) error {
	return memory.ErrVersionConflict
}

func (conflictingStore) DeleteVersioned(context.Context, string, string, string) error {
	return memory.ErrVersionConflict
}

func TestFileStoreWriteDeleteAndErrors(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := memory.NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	ctx := context.Background()
	if err := store.Write(ctx, "", "MEMORY.md", "one"); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := store.Write(ctx, "", "MEMORY.md", "two"); err != nil {
		t.Fatalf("overwrite error = %v", err)
	}
	if _, err := store.Read(ctx, "", "missing.md"); !errors.Is(err, memory.ErrNotFound) {
		t.Fatalf("missing read error = %v", err)
	}
	if _, err := store.Read(ctx, "", ""); err == nil {
		t.Fatal("empty path accepted")
	}
	if err := store.Write(ctx, "../bad", "MEMORY.md", "x"); err == nil {
		t.Fatal("invalid namespace accepted")
	}
	if err := store.Write(ctx, "", "../bad.md", "x"); err == nil {
		t.Fatal("traversal path accepted")
	}
	if err := os.MkdirAll(filepath.Join(root, "dir.md"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := store.Read(ctx, "", "dir.md"); err == nil {
		t.Fatal("directory read accepted")
	}
	if _, err := store.List(ctx, "MEMORY.md"); err == nil {
		t.Fatal("file namespace accepted")
	}
	if err := store.Delete(ctx, "", "missing.md"); !errors.Is(err, memory.ErrNotFound) {
		t.Fatalf("missing delete error = %v", err)
	}
	if err := store.DeleteVersioned(ctx, "", "missing.md", "v"); !errors.Is(err, memory.ErrNotFound) {
		t.Fatalf("missing versioned delete error = %v", err)
	}
	if err := store.Delete(ctx, "", "MEMORY.md"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := store.List(ctx, "absent"); err != nil {
		t.Fatalf("absent list error = %v", err)
	}
}

func TestNewFileStoreRejectsBadRoot(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(root, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if _, err := memory.NewFileStore(root); err == nil {
		t.Fatal("file root accepted")
	}
	if _, err := memory.NewFileStore("  "); err == nil {
		t.Fatal("empty root accepted")
	}
}

func TestToolsWorkWithPlainStore(t *testing.T) {
	t.Parallel()

	store := &basicStore{files: map[string]string{}}
	tools := newTools(t, memory.Config[struct{}]{Store: store})
	exec(t, tools, "write_memory", struct{}{}, `{"path":"notes/plain.md","content":"one"}`)
	if got := exec(t, tools, "read_memory", struct{}{}, `{"path":"notes/plain.md"}`); got != "one" {
		t.Fatalf("read = %q", got)
	}
	exec(t, tools, "delete_memory", struct{}{}, `{"path":"notes/plain.md"}`)
	if _, err := execErr(tools, "read_memory", struct{}{}, `{"path":"notes/plain.md"}`); err == nil {
		t.Fatal("delete through plain store failed")
	}
}

func TestMemoryDeleteVersionConflict(t *testing.T) {
	t.Parallel()

	store := memory.NewMemory()
	ctx := context.Background()
	if err := store.WriteVersioned(ctx, "", "MEMORY.md", "one", ""); err != nil {
		t.Fatalf("create: %v", err)
	}
	file, _ := store.Read(ctx, "", "MEMORY.md")
	if err := store.WriteVersioned(ctx, "", "MEMORY.md", "two", file.Version); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := store.DeleteVersioned(ctx, "", "MEMORY.md", file.Version); !errors.Is(err, memory.ErrVersionConflict) {
		t.Fatalf("stale delete error = %v", err)
	}
}

func TestFileStoreWriteError(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := memory.NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	if err := os.Chmod(root, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o700) })
	if err := store.Write(context.Background(), "", "MEMORY.md", "x"); err == nil {
		t.Fatal("write into read-only root succeeded")
	}
}

type basicStore struct{ files map[string]string }

func (s *basicStore) List(context.Context, string) ([]string, error) {
	var paths []string
	for path := range s.files {
		paths = append(paths, path)
	}
	return paths, nil
}

func (s *basicStore) Read(_ context.Context, _, path string) (memory.File, error) {
	content, ok := s.files[path]
	if !ok {
		return memory.File{}, memory.ErrNotFound
	}
	return memory.File{Path: path, Content: content}, nil
}

func (s *basicStore) Write(_ context.Context, _, path, content string) error {
	s.files[path] = content
	return nil
}

func (s *basicStore) Delete(_ context.Context, _, path string) error {
	if _, ok := s.files[path]; !ok {
		return memory.ErrNotFound
	}
	delete(s.files, path)
	return nil
}

func TestMemoryVersionedEdges(t *testing.T) {
	t.Parallel()

	store := memory.NewMemory()
	ctx := context.Background()
	if err := store.WriteVersioned(ctx, "", "MEMORY.md", "one", ""); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.WriteVersioned(ctx, "", "MEMORY.md", "again", ""); !errors.Is(err, memory.ErrVersionConflict) {
		t.Fatalf("second create error = %v", err)
	}
	file, _ := store.Read(ctx, "", "MEMORY.md")
	if err := store.DeleteVersioned(ctx, "", "MEMORY.md", file.Version); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := store.DeleteVersioned(ctx, "", "MEMORY.md", file.Version); !errors.Is(err, memory.ErrVersionConflict) {
		t.Fatalf("missing delete error = %v", err)
	}
}

func TestFileStoreDeleteVersioned(t *testing.T) {
	t.Parallel()

	store, err := memory.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	ctx := context.Background()
	if err := store.WriteVersioned(ctx, "ada", "notes/a.md", "x", ""); err != nil {
		t.Fatalf("create: %v", err)
	}
	file, err := store.Read(ctx, "ada", "notes/a.md")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := store.DeleteVersioned(ctx, "ada", "notes/a.md", file.Version); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.Read(ctx, "ada", "notes/a.md"); !errors.Is(err, memory.ErrNotFound) {
		t.Fatalf("after delete read error = %v", err)
	}
}

func TestToolReportsDeleteVersionConflict(t *testing.T) {
	t.Parallel()

	tools := newTools(t, memory.Config[struct{}]{Store: conflictingStore{Memory: memory.NewMemory()}})
	_, err := execErr(tools, "delete_memory", struct{}{}, `{"path":"notes/a.md"}`)
	var failed *tool.Failed
	if !errors.As(err, &failed) || !strings.Contains(failed.Reason, "changed concurrently") {
		t.Fatalf("delete error = %v", err)
	}
}

func TestFileStoreWriteToDirectoryFails(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := memory.NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "dir.md"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := store.Write(context.Background(), "", "dir.md", "x"); err == nil {
		t.Fatal("write over directory succeeded")
	}
	if err := store.WriteVersioned(context.Background(), "", "dir.md", "x", "v"); err == nil {
		t.Fatal("versioned write over directory succeeded")
	}
}

func TestFileStoreListNested(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := memory.NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	ctx := context.Background()
	for _, path := range []string{"a.md", "notes/b.md", "notes/deep/c.md"} {
		if err := store.Write(ctx, "ns", path, path); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	paths, err := store.List(ctx, "ns")
	if err != nil || len(paths) != 3 || paths[0] != "a.md" || paths[2] != "notes/deep/c.md" {
		t.Fatalf("List() = %v, %v", paths, err)
	}
}

func TestMemoryPlainDelete(t *testing.T) {
	t.Parallel()

	store := memory.NewMemory()
	ctx := context.Background()
	if err := store.Write(ctx, "ns", "MEMORY.md", "x"); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := store.Delete(ctx, "ns", "MEMORY.md"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := store.List(ctx, "ns"); err != nil {
		t.Fatalf("List() after delete error = %v", err)
	}
	if err := store.Delete(ctx, "ns", "MEMORY.md"); !errors.Is(err, memory.ErrNotFound) {
		t.Fatalf("second delete error = %v", err)
	}
}
