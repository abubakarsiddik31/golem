package memory_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem/memory"
	"github.com/abubakarsiddik31/golem/tool"
)

func newTools[Deps any](t *testing.T, cfg memory.Config[Deps]) []tool.Tool[Deps] {
	t.Helper()
	tools, err := memory.Tools(cfg)
	if err != nil {
		t.Fatalf("Tools() error = %v", err)
	}
	return tools
}

func exec[Deps any](t *testing.T, tools []tool.Tool[Deps], name string, deps Deps, args string) string {
	t.Helper()
	result, err := execErr(tools, name, deps, args)
	if err != nil {
		t.Fatalf("%s error = %v", name, err)
	}
	return result.Text
}

func execErr[Deps any](tools []tool.Tool[Deps], name string, deps Deps, args string) (tool.Result, error) {
	for _, candidate := range tools {
		if candidate.Name == name {
			return candidate.Exec(context.Background(), deps, json.RawMessage(args))
		}
	}
	return tool.Result{}, errors.New("test: tool not found: " + name)
}

func TestToolsRequireAStore(t *testing.T) {
	t.Parallel()

	if _, err := memory.Tools[struct{}](memory.Config[struct{}]{}); err == nil {
		t.Fatal("nil Store accepted")
	}
}

func TestNotebookLifecycle(t *testing.T) {
	t.Parallel()

	tools := newTools(t, memory.Config[struct{}]{Store: memory.NewMemory()})
	if got := exec(t, tools, "list_memory", struct{}{}, `{}`); got != "No memory files." {
		t.Fatalf("empty list = %q", got)
	}

	exec(t, tools, "write_memory", struct{}{}, `{"path":"MEMORY.md","content":"first"}`)
	exec(t, tools, "write_memory", struct{}{}, `{"path":"MEMORY.md","content":" second"}`)
	if got := exec(t, tools, "read_memory", struct{}{}, `{"path":"MEMORY.md"}`); got != "first second" {
		t.Fatalf("appended content = %q", got)
	}

	exec(t, tools, "write_memory", struct{}{}, `{"path":"MEMORY.md","mode":"replace","content":"reset"}`)
	if got := exec(t, tools, "read_memory", struct{}{}, `{"path":"MEMORY.md"}`); got != "reset" {
		t.Fatalf("replaced content = %q", got)
	}

	exec(t, tools, "write_memory", struct{}{}, `{"path":"MEMORY.md","mode":"edit","oldText":"reset","content":"final"}`)
	if got := exec(t, tools, "read_memory", struct{}{}, `{"path":"MEMORY.md"}`); got != "final" {
		t.Fatalf("edited content = %q", got)
	}

	exec(t, tools, "write_memory", struct{}{}, `{"path":"notes/auth.md","content":"Use the OAuth token flow."}`)
	list := exec(t, tools, "list_memory", struct{}{}, `{}`)
	if !strings.Contains(list, "MEMORY.md") || !strings.Contains(list, "notes/auth.md") {
		t.Fatalf("list = %q", list)
	}

	search := exec(t, tools, "search_memory", struct{}{}, `{"query":"oauth"}`)
	if !strings.Contains(search, "notes/auth.md") || !strings.Contains(search, "OAuth token flow") {
		t.Fatalf("search = %q", search)
	}

	if got := exec(t, tools, "delete_memory", struct{}{}, `{"path":"notes/auth.md"}`); !strings.Contains(got, "Deleted notes/auth.md") {
		t.Fatalf("delete = %q", got)
	}
	if got := exec(t, tools, "search_memory", struct{}{}, `{"query":"oauth"}`); got != "No matches." {
		t.Fatalf("search after delete = %q", got)
	}

	_, err := execErr(tools, "delete_memory", struct{}{}, `{"path":"MEMORY.md"}`)
	var failed *tool.Failed
	if !errors.As(err, &failed) || !strings.Contains(failed.Reason, "cannot be deleted") {
		t.Fatalf("deleting MEMORY.md error = %v", err)
	}
}

func TestEditFailures(t *testing.T) {
	t.Parallel()

	tools := newTools(t, memory.Config[struct{}]{Store: memory.NewMemory()})
	exec(t, tools, "write_memory", struct{}{}, `{"path":"MEMORY.md","content":"one one two"}`)

	cases := []string{
		`{"path":"MEMORY.md","mode":"edit","oldText":"missing","content":"x"}`,
		`{"path":"MEMORY.md","mode":"edit","oldText":"one","content":"x"}`,
		`{"path":"MEMORY.md","mode":"edit","content":"x"}`,
		`{"path":"missing.md","mode":"edit","oldText":"one","content":"x"}`,
		`{"path":"MEMORY.md","mode":"unknown","content":"x"}`,
	}
	for _, args := range cases {
		_, err := execErr(tools, "write_memory", struct{}{}, args)
		var failed *tool.Failed
		if !errors.As(err, &failed) {
			t.Fatalf("args %s: error = %v, want *tool.Failed", args, err)
		}
	}
}

func TestPathValidationRejectsEscapes(t *testing.T) {
	t.Parallel()

	tools := newTools(t, memory.Config[struct{}]{Store: memory.NewMemory()})
	paths := []string{"", "../MEMORY.md", "/MEMORY.md", "notes/../MEMORY.md", "notes//auth.md", "notes\\auth.md", "notes/ auth.md", "notes/"}
	for _, path := range paths {
		_, err := execErr(tools, "read_memory", struct{}{}, `{"path":"`+path+`"}`)
		var failed *tool.Failed
		if !errors.As(err, &failed) {
			t.Fatalf("path %q: error = %v, want *tool.Failed", path, err)
		}
	}
}

func TestNamespaceScopesNotebooks(t *testing.T) {
	t.Parallel()

	type deps struct{ Tenant string }
	tools := newTools(t, memory.Config[deps]{
		Store:     memory.NewMemory(),
		Namespace: func(d deps) string { return d.Tenant },
	})
	exec(t, tools, "write_memory", deps{Tenant: "alice"}, `{"path":"MEMORY.md","content":"alice note"}`)

	if got := exec(t, tools, "list_memory", deps{Tenant: "bob"}, `{}`); got != "No memory files." {
		t.Fatalf("bob's notebook = %q", got)
	}
	if got := exec(t, tools, "read_memory", deps{Tenant: "alice"}, `{"path":"MEMORY.md"}`); got != "alice note" {
		t.Fatalf("alice's note = %q", got)
	}

	_, err := execErr(tools, "list_memory", deps{Tenant: "../bob"}, `{}`)
	var failed *tool.Failed
	if errors.As(err, &failed) {
		t.Fatalf("invalid namespace was reported as model input: %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "namespace") {
		t.Fatalf("invalid namespace error = %v", err)
	}
}

func TestSizeBounds(t *testing.T) {
	t.Parallel()

	store := memory.NewMemory()
	tools := newTools(t, memory.Config[struct{}]{Store: store, MaxFileBytes: 10})

	_, err := execErr(tools, "write_memory", struct{}{}, `{"path":"MEMORY.md","content":"01234567890"}`)
	var failed *tool.Failed
	if !errors.As(err, &failed) || !strings.Contains(failed.Reason, "byte bound") {
		t.Fatalf("oversized write error = %v", err)
	}
	exec(t, tools, "write_memory", struct{}{}, `{"path":"MEMORY.md","content":"0123456789"}`)
	_, err = execErr(tools, "write_memory", struct{}{}, `{"path":"MEMORY.md","content":"x"}`)
	if !errors.As(err, &failed) || !strings.Contains(failed.Reason, "over the") {
		t.Fatalf("oversized append error = %v", err)
	}

	// An external writer can leave a larger file; reads are bounded and
	// marked, while append refuses rather than building from a prefix.
	if err := store.Write(context.Background(), "", "large.md", strings.Repeat("a", 25)); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	read := exec(t, tools, "read_memory", struct{}{}, `{"path":"large.md"}`)
	if len(read) > 60 || !strings.Contains(read, "[truncated at the read bound]") {
		t.Fatalf("bounded read = %q", read)
	}
	_, err = execErr(tools, "write_memory", struct{}{}, `{"path":"large.md","content":"x"}`)
	if !errors.As(err, &failed) {
		t.Fatalf("append to oversized file error = %v", err)
	}
}

func TestSearchBounds(t *testing.T) {
	t.Parallel()

	store := memory.NewMemory()
	tools := newTools(t, memory.Config[struct{}]{
		Store:          store,
		MaxResults:     1,
		MaxSearchFiles: 1,
		MaxResultChars: 15,
	})
	exec(t, tools, "write_memory", struct{}{}, `{"path":"a.md","content":"term in a"}`)
	exec(t, tools, "write_memory", struct{}{}, `{"path":"b.md","content":"term in b"}`)

	got := exec(t, tools, "search_memory", struct{}{}, `{"query":"term","limit":5}`)
	if strings.Contains(got, "b.md") {
		t.Fatalf("search scanned past MaxSearchFiles or returned past MaxResults: %q", got)
	}
	if !strings.Contains(got, "[more matches omitted at the result bound]") && len(got) > 15 {
		t.Fatalf("result bound not applied: %q", got)
	}

	for _, args := range []string{`{"query":"  "}`, `{"query":"x","limit":-1}`, `{`} {
		_, err := execErr(tools, "search_memory", struct{}{}, args)
		var failed *tool.Failed
		if !errors.As(err, &failed) {
			t.Fatalf("args %s: error = %v, want *tool.Failed", args, err)
		}
	}
}

func TestStoreFailureAborts(t *testing.T) {
	t.Parallel()

	tools := newTools(t, memory.Config[struct{}]{Store: failingStore{}})
	_, err := execErr(tools, "list_memory", struct{}{}, `{}`)
	if err == nil || !strings.Contains(err.Error(), "store offline") {
		t.Fatalf("error = %v, want the wrapped store failure", err)
	}
	var failed *tool.Failed
	if errors.As(err, &failed) {
		t.Fatal("infrastructure failure was reported as model input")
	}
}

func TestMemoryStoreContract(t *testing.T) {
	t.Parallel()

	var store memory.Memory
	ctx := context.Background()
	if _, err := store.Read(ctx, "ns", "missing.md"); !errors.Is(err, memory.ErrNotFound) {
		t.Fatalf("Read() error = %v, want ErrNotFound", err)
	}
	if err := store.Write(ctx, "ns", "b.md", "b"); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := store.Write(ctx, "ns", "a.md", "a"); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	paths, err := store.List(ctx, "ns")
	if err != nil || len(paths) != 2 || paths[0] != "a.md" || paths[1] != "b.md" {
		t.Fatalf("List() = %v, %v", paths, err)
	}
	if err := store.Delete(ctx, "ns", "missing.md"); !errors.Is(err, memory.ErrNotFound) {
		t.Fatalf("Delete() error = %v, want ErrNotFound", err)
	}
}

type failingStore struct{}

func (failingStore) List(context.Context, string) ([]string, error) {
	return nil, errors.New("store offline")
}

func (failingStore) Read(context.Context, string, string) (memory.File, error) {
	return memory.File{}, errors.New("store offline")
}

func (failingStore) Write(context.Context, string, string, string) error {
	return errors.New("store offline")
}

func (failingStore) Delete(context.Context, string, string) error {
	return errors.New("store offline")
}
