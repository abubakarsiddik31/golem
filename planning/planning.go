// Package planning gives an agent a task plan it maintains through
// tools: create or replace the whole plan, append a step, move a step
// between statuses, and remove one. The plan lives behind a small Store
// port, so an application can keep it in memory for one run or persist
// it across runs; Memory is the offline implementation.
//
// Every mutating tool returns the full plan as it now stands, rendered
// as a checklist in the tool result. The latest plan therefore rides the
// conversation's tool channel rather than the system prompt, so editing
// it does not change the cached prompt prefix. The package never reads
// the working directory, environment, or clock: the application chooses
// the store and the key that scopes a plan to a tenant, conversation, or
// run.
//
// The tools are ordinary tool.Tool[Deps] values and compose with any
// dependency type. Their names, argument schemas, and result shape are
// public contract: models and prompts depend on them staying stable.
package planning

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/abubakarsiddik31/golem/tool"
)

// Status is one step's lifecycle state.
type Status string

const (
	// Pending is a step that has not started.
	Pending Status = "pending"
	// InProgress is the step the agent is working on now.
	InProgress Status = "in_progress"
	// Completed is a finished step.
	Completed Status = "completed"
	// Cancelled is a step the agent abandoned.
	Cancelled Status = "cancelled"
)

// Instructions is the guidance callers can add to an agent's
// instructions. It states the convention the tools assume — keep one
// step in progress and update the plan as work advances — without
// repeating it in every tool description. The package does not install
// it automatically; agent instructions stay the application's.
const Instructions = "Track multi-step work with the plan tools. Keep exactly one step in_progress at a time, mark steps completed as you finish them, and cancel steps you abandon. The latest plan tool result shows the full plan."

// Item is one step of a plan. ID is stable for the life of the item and
// is what the status and removal tools accept. ActiveForm is an optional
// present-continuous label, such as "Running the tests", shown while the
// step is in progress.
type Item struct {
	ID         string `json:"id"`
	Content    string `json:"content"`
	ActiveForm string `json:"activeForm,omitempty"`
	Status     Status `json:"status"`
}

// Plan is an ordered task list. Items are rendered in slice order.
type Plan struct {
	Items []Item `json:"items"`
}

// Store loads and saves the plan for one key. Keys scope plans to a
// tenant, conversation, or run; an empty key is a valid single plan.
// Implementations must be safe for concurrent use when the toolset is
// shared, and must treat a missing key as an empty plan rather than an
// error.
type Store interface {
	Load(ctx context.Context, key string) (Plan, error)
	Save(ctx context.Context, key string, plan Plan) error
}

// Memory is an in-process Store. It is safe for concurrent use. Plans
// live only as long as the value; a process restart starts empty.
type Memory struct {
	mu    sync.Mutex
	plans map[string]Plan
}

// NewMemory returns an empty in-process store.
func NewMemory() *Memory {
	return &Memory{plans: make(map[string]Plan)}
}

// Load returns a copy of the plan stored under key, or an empty plan.
func (m *Memory) Load(_ context.Context, key string) (Plan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return clonePlan(m.plans[key]), nil
}

// Save stores a copy of plan under key.
func (m *Memory) Save(_ context.Context, key string, plan Plan) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.plans == nil {
		m.plans = make(map[string]Plan)
	}
	m.plans[key] = clonePlan(plan)
	return nil
}

// Config configures a planning toolset.
type Config[Deps any] struct {
	// Store holds the plan. It is required.
	Store Store
	// Key selects the plan for a run's dependencies. A nil Key uses the
	// empty key, which is one shared plan per store.
	Key func(deps Deps) string
}

// Tools returns the planning toolset: write_plan, read_plan, add_task,
// update_task_status, and remove_task. Mutating tools return the full
// plan in their result. Store failures abort the run as tool errors; bad
// model input (empty content, an unknown ID or status) returns
// &tool.Failed so the model can correct the call without failing the
// run.
func Tools[Deps any](cfg Config[Deps]) ([]tool.Tool[Deps], error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("planning: Store is required")
	}
	p := &planner[Deps]{cfg: cfg}
	write, err := tool.New(tool.Tool[Deps]{
		Name:        "write_plan",
		Description: "Create or replace the task plan from an ordered list of steps. Use this for multi-step work, then keep exactly one step in_progress.",
		Schema:      writeSchema,
		Exec:        p.write,
	})
	if err != nil {
		return nil, err
	}
	read, err := tool.New(tool.Tool[Deps]{
		Name:        "read_plan",
		Description: "Read the current task plan with step IDs, statuses, and a progress summary.",
		Schema:      emptySchema,
		Exec:        p.read,
	})
	if err != nil {
		return nil, err
	}
	add, err := tool.New(tool.Tool[Deps]{
		Name:        "add_task",
		Description: "Append one pending step to the task plan and return the full plan.",
		Schema:      addSchema,
		Exec:        p.add,
	})
	if err != nil {
		return nil, err
	}
	update, err := tool.New(tool.Tool[Deps]{
		Name:        "update_task_status",
		Description: "Move one plan step to pending, in_progress, completed, or cancelled by ID, and return the full plan. Keep exactly one step in_progress.",
		Schema:      updateSchema,
		Exec:        p.update,
	})
	if err != nil {
		return nil, err
	}
	remove, err := tool.New(tool.Tool[Deps]{
		Name:        "remove_task",
		Description: "Remove one plan step by ID and return the full plan.",
		Schema:      removeSchema,
		Exec:        p.remove,
	})
	if err != nil {
		return nil, err
	}
	return []tool.Tool[Deps]{write, read, add, update, remove}, nil
}

// planner holds the store and the per-toolset lock that makes one
// load-mutate-save step atomic within this toolset.
type planner[Deps any] struct {
	cfg Config[Deps]
	mu  sync.Mutex
}

func (p *planner[Deps]) key(deps Deps) string {
	if p.cfg.Key == nil {
		return ""
	}
	return p.cfg.Key(deps)
}

func (p *planner[Deps]) load(ctx context.Context, key string) (Plan, error) {
	plan, err := p.cfg.Store.Load(ctx, key)
	if err != nil {
		return Plan{}, fmt.Errorf("planning: load plan: %w", err)
	}
	return plan, nil
}

func (p *planner[Deps]) save(ctx context.Context, key string, plan Plan) error {
	if err := p.cfg.Store.Save(ctx, key, plan); err != nil {
		return fmt.Errorf("planning: save plan: %w", err)
	}
	return nil
}

// mutate serializes one load-mutate-save step for this toolset and
// returns the message plus the resulting plan.
func (p *planner[Deps]) mutate(ctx context.Context, deps Deps, fn func(*Plan) (string, error)) (tool.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := p.key(deps)
	plan, err := p.load(ctx, key)
	if err != nil {
		return tool.Result{}, err
	}
	message, err := fn(&plan)
	if err != nil {
		return tool.Result{}, err
	}
	if err := p.save(ctx, key, plan); err != nil {
		return tool.Result{}, err
	}
	return tool.Text(message + "\n\n" + render(plan)), nil
}

func (p *planner[Deps]) write(ctx context.Context, deps Deps, args json.RawMessage) (tool.Result, error) {
	var input struct {
		Items []struct {
			Content    string `json:"content"`
			ActiveForm string `json:"activeForm"`
		} `json:"items"`
	}
	if err := decode(args, &input); err != nil {
		return fail(err)
	}
	plan := Plan{Items: make([]Item, 0, len(input.Items))}
	for i, item := range input.Items {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			return fail(fmt.Errorf("item %d has empty content", i+1))
		}
		plan.Items = append(plan.Items, Item{
			ID:         strconv.Itoa(i + 1),
			Content:    content,
			ActiveForm: strings.TrimSpace(item.ActiveForm),
			Status:     Pending,
		})
	}
	message := fmt.Sprintf("Wrote a plan with %d step(s).", len(plan.Items))
	return p.mutate(ctx, deps, func(stored *Plan) (string, error) {
		*stored = plan
		return message, nil
	})
}

func (p *planner[Deps]) read(ctx context.Context, deps Deps, _ json.RawMessage) (tool.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	plan, err := p.load(ctx, p.key(deps))
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Text(render(plan)), nil
}

func (p *planner[Deps]) add(ctx context.Context, deps Deps, args json.RawMessage) (tool.Result, error) {
	var input struct {
		Content    string `json:"content"`
		ActiveForm string `json:"activeForm"`
	}
	if err := decode(args, &input); err != nil {
		return fail(err)
	}
	content := strings.TrimSpace(input.Content)
	if content == "" {
		return fail(fmt.Errorf("content is required"))
	}
	return p.mutate(ctx, deps, func(plan *Plan) (string, error) {
		id := nextID(*plan)
		plan.Items = append(plan.Items, Item{
			ID:         id,
			Content:    content,
			ActiveForm: strings.TrimSpace(input.ActiveForm),
			Status:     Pending,
		})
		return fmt.Sprintf("Added step %s.", id), nil
	})
}

func (p *planner[Deps]) update(ctx context.Context, deps Deps, args json.RawMessage) (tool.Result, error) {
	var input struct {
		TaskID string `json:"taskId"`
		Status Status `json:"status"`
	}
	if err := decode(args, &input); err != nil {
		return fail(err)
	}
	if !input.Status.valid() {
		return fail(fmt.Errorf("status %q is not one of pending, in_progress, completed, cancelled", input.Status))
	}
	return p.mutate(ctx, deps, func(plan *Plan) (string, error) {
		for i := range plan.Items {
			if plan.Items[i].ID != input.TaskID {
				continue
			}
			plan.Items[i].Status = input.Status
			return fmt.Sprintf("Moved step %s to %s.", input.TaskID, input.Status), nil
		}
		return "", &tool.Failed{Reason: unknownTask(*plan, input.TaskID)}
	})
}

func (p *planner[Deps]) remove(ctx context.Context, deps Deps, args json.RawMessage) (tool.Result, error) {
	var input struct {
		TaskID string `json:"taskId"`
	}
	if err := decode(args, &input); err != nil {
		return fail(err)
	}
	return p.mutate(ctx, deps, func(plan *Plan) (string, error) {
		for i := range plan.Items {
			if plan.Items[i].ID != input.TaskID {
				continue
			}
			plan.Items = append(plan.Items[:i], plan.Items[i+1:]...)
			return fmt.Sprintf("Removed step %s.", input.TaskID), nil
		}
		return "", &tool.Failed{Reason: unknownTask(*plan, input.TaskID)}
	})
}

func (s Status) valid() bool {
	switch s {
	case Pending, InProgress, Completed, Cancelled:
		return true
	default:
		return false
	}
}

func decode(args json.RawMessage, target any) error {
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(args, target); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

func fail(err error) (tool.Result, error) {
	return tool.Result{}, &tool.Failed{Reason: err.Error()}
}

func unknownTask(plan Plan, id string) string {
	if len(plan.Items) == 0 {
		return fmt.Sprintf("task %q does not exist; the plan is empty", id)
	}
	ids := make([]string, len(plan.Items))
	for i, item := range plan.Items {
		ids[i] = item.ID
	}
	return fmt.Sprintf("task %q does not exist; known IDs: %s", id, strings.Join(ids, ", "))
}

func nextID(plan Plan) string {
	max := 0
	for _, item := range plan.Items {
		if n, err := strconv.Atoi(item.ID); err == nil && n > max {
			max = n
		}
	}
	if max < len(plan.Items) {
		max = len(plan.Items)
	}
	return strconv.Itoa(max + 1)
}

func render(plan Plan) string {
	if len(plan.Items) == 0 {
		return "The plan is empty."
	}
	completed := 0
	for _, item := range plan.Items {
		if item.Status == Completed {
			completed++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Plan (%d/%d completed):", completed, len(plan.Items))
	for _, item := range plan.Items {
		fmt.Fprintf(&b, "\n%s. [%s] %s", item.ID, item.Status, oneLine(item.Content))
		if item.ActiveForm != "" && item.Status == InProgress {
			fmt.Fprintf(&b, " (%s)", oneLine(item.ActiveForm))
		}
	}
	return b.String()
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func clonePlan(plan Plan) Plan {
	if plan.Items == nil {
		return Plan{}
	}
	items := make([]Item, len(plan.Items))
	copy(items, plan.Items)
	return Plan{Items: items}
}

var emptySchema = json.RawMessage(`{
	"type": "object",
	"properties": {},
	"additionalProperties": false
}`)

var writeSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"items": {
			"type": "array",
			"description": "Ordered plan steps; replaces the current plan.",
			"items": {
				"type": "object",
				"properties": {
					"content": {"type": "string", "description": "Imperative step text."},
					"activeForm": {"type": "string", "description": "Optional present-continuous label shown while in progress."}
				},
				"required": ["content"],
				"additionalProperties": false
			}
		}
	},
	"required": ["items"],
	"additionalProperties": false
}`)

var addSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"content": {"type": "string", "description": "Imperative step text."},
		"activeForm": {"type": "string", "description": "Optional present-continuous label shown while in progress."}
	},
	"required": ["content"],
	"additionalProperties": false
}`)

var updateSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"taskId": {"type": "string", "description": "Step ID from the current plan."},
		"status": {"type": "string", "enum": ["pending", "in_progress", "completed", "cancelled"]}
	},
	"required": ["taskId", "status"],
	"additionalProperties": false
}`)

var removeSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"taskId": {"type": "string", "description": "Step ID from the current plan."}
	},
	"required": ["taskId"],
	"additionalProperties": false
}`)
