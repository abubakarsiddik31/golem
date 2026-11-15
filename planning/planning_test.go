package planning_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem/planning"
	"github.com/abubakarsiddik31/golem/tool"
)

func TestToolsRequireAStore(t *testing.T) {
	t.Parallel()

	if _, err := planning.Tools[struct{}](planning.Config[struct{}]{}); err == nil {
		t.Fatal("nil Store accepted")
	}
}

func TestPlanLifecycle(t *testing.T) {
	t.Parallel()

	tools := newTools(t, planning.Config[struct{}]{Store: planning.NewMemory()})

	write := exec(t, tools, "write_plan", struct{}{}, `{"items":[
		{"content":"Read the issue","activeForm":"Reading the issue"},
		{"content":"Write the tests"}
	]}`)
	if !strings.Contains(write, "Plan (0/2 completed)") || !strings.Contains(write, "1. [pending] Read the issue") {
		t.Fatalf("write_plan result = %q", write)
	}

	read := exec(t, tools, "read_plan", struct{}{}, `{}`)
	if !strings.HasSuffix(write, read) {
		t.Fatalf("read_plan = %q, want the plan appended to write_plan %q", read, write)
	}

	add := exec(t, tools, "add_task", struct{}{}, `{"content":"Ship it"}`)
	if !strings.Contains(add, "Added step 3.") || !strings.Contains(add, "3. [pending] Ship it") {
		t.Fatalf("add_task result = %q", add)
	}

	update := exec(t, tools, "update_task_status", struct{}{}, `{"taskId":"1","status":"in_progress"}`)
	if !strings.Contains(update, "1. [in_progress] Read the issue (Reading the issue)") {
		t.Fatalf("update_task_status result = %q", update)
	}

	update = exec(t, tools, "update_task_status", struct{}{}, `{"taskId":"1","status":"completed"}`)
	if !strings.Contains(update, "Plan (1/3 completed)") {
		t.Fatalf("completed result = %q", update)
	}

	remove := exec(t, tools, "remove_task", struct{}{}, `{"taskId":"2"}`)
	if strings.Contains(remove, "2. ") || !strings.Contains(remove, "3. [pending] Ship it") {
		t.Fatalf("remove_task result = %q", remove)
	}

	updated := exec(t, tools, "add_task", struct{}{}, `{"content":"Final check"}`)
	if !strings.Contains(updated, "Added step 4.") {
		t.Fatalf("IDs must not be reused after removal: %q", updated)
	}
}

func TestKeyScopesPlans(t *testing.T) {
	t.Parallel()

	type deps struct{ Tenant string }
	tools := newTools(t, planning.Config[deps]{
		Store: planning.NewMemory(),
		Key:   func(d deps) string { return d.Tenant },
	})

	exec(t, tools, "write_plan", deps{Tenant: "alice"}, `{"items":[{"content":"Alice's step"}]}`)
	read := exec(t, tools, "read_plan", deps{Tenant: "bob"}, `{}`)
	if read != "The plan is empty." {
		t.Fatalf("bob's plan = %q, want an isolated empty plan", read)
	}
	read = exec(t, tools, "read_plan", deps{Tenant: "alice"}, `{}`)
	if !strings.Contains(read, "Alice's step") {
		t.Fatalf("alice's plan = %q", read)
	}
}

func TestBadModelInputIsRecoverable(t *testing.T) {
	t.Parallel()

	tools := newTools(t, planning.Config[struct{}]{Store: planning.NewMemory()})
	exec(t, tools, "write_plan", struct{}{}, `{"items":[{"content":"one"}]}`)

	cases := []struct {
		name string
		tool string
		args string
	}{
		{name: "empty content", tool: "add_task", args: `{"content":"  "}`},
		{name: "empty item", tool: "write_plan", args: `{"items":[{"content":""}]}`},
		{name: "unknown id", tool: "update_task_status", args: `{"taskId":"9","status":"completed"}`},
		{name: "unknown remove", tool: "remove_task", args: `{"taskId":"9"}`},
		{name: "bad status", tool: "update_task_status", args: `{"taskId":"1","status":"done"}`},
		{name: "malformed json", tool: "add_task", args: `{`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := execErr(tools, tc.tool, struct{}{}, tc.args)
			var failed *tool.Failed
			if !errors.As(err, &failed) {
				t.Fatalf("error = %v, want *tool.Failed so the model can correct the call", err)
			}
			if failed.Reason == "" {
				t.Fatal("failure carries no reason")
			}
		})
	}
}

func TestStoreFailureAbortsTheTool(t *testing.T) {
	t.Parallel()

	tools := newTools(t, planning.Config[struct{}]{Store: failingStore{}})
	_, err := execErr(tools, "read_plan", struct{}{}, `{}`)
	if err == nil || !strings.Contains(err.Error(), "load plan") {
		t.Fatalf("error = %v, want the wrapped store failure", err)
	}
	var failed *tool.Failed
	if errors.As(err, &failed) {
		t.Fatal("infrastructure failure was reported as recoverable model input")
	}
}

func newTools[Deps any](t *testing.T, cfg planning.Config[Deps]) []tool.Tool[Deps] {
	t.Helper()
	tools, err := planning.Tools(cfg)
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

type failingStore struct{}

func (failingStore) Load(context.Context, string) (planning.Plan, error) {
	return planning.Plan{}, errors.New("store offline")
}

func (failingStore) Save(context.Context, string, planning.Plan) error {
	return errors.New("store offline")
}
