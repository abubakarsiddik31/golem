# Planning

## Purpose

Give an agent a task list it owns and updates through tools, so a long
run keeps track of what it set out to do and what is left.

## When to use

Multi-step work where the model must plan, execute, and revise — a
research task, a refactor, a debugging session. For a single-turn
answer the plan tools only add prompt weight; for deterministic
orchestration, write the pipeline in Go with the
[Orchestration](orchestration.md) guide instead.

## How it works

`planning.Tools` returns five ordinary tools:

- `write_plan` creates or replaces the whole plan from ordered steps.
- `read_plan` returns the plan with step IDs, statuses, and progress.
- `add_task` appends one pending step.
- `update_task_status` moves one step between `pending`, `in_progress`,
  `completed`, and `cancelled`.
- `remove_task` deletes one step.

Step IDs are stable strings; removal never reuses an ID. Every mutating
tool ends its result with the full plan rendered as a checklist, so the
latest plan rides the conversation's tool channel instead of the system
prompt. Editing the plan therefore does not change the cached prompt
prefix.

The plan lives behind `planning.Store`, which the application supplies:
`Load(ctx, key)` and `Save(ctx, key, plan)`. `Config.Key` derives the
key from the run's dependencies, so one store can hold one plan per
tenant or conversation; a nil `Key` uses a single empty key.
`planning.Memory` is the in-process implementation. The package never
reads the environment, clock, or working directory.

`planning.Instructions` is the guidance the tools assume — keep one step
in progress and update the plan as work advances. The package does not
install it; add it with `golem.WithInstructions` so the agent's
instructions stay the application's decision.

Bad model input — empty content, an unknown ID, an unsupported status,
malformed JSON — returns `&tool.Failed`, so the model sees the reason
and can correct the call without aborting the run. A store failure is
an infrastructure error and aborts the run at the tool stage.

## Example

[`examples/planning`](../../examples/planning/main.go) runs offline
against a scripted model and prints each plan tool result:

```go
tools, err := planning.Tools(planning.Config[struct{}]{Store: planning.NewMemory()})
agent, err := golem.New[struct{}, string](client, decoder,
	golem.WithInstructions[struct{}, string](planning.Instructions),
	golem.WithTools[struct{}, string](tools...),
)
```

## API surface

- `planning.Tools[Deps](planning.Config[Deps]) ([]tool.Tool[Deps], error)`
- `planning.Config[Deps]{Store Store, Key func(Deps) string}`
- `planning.Store` — `Load(ctx, key) (Plan, error)` and `Save(ctx, key, Plan) error`
- `planning.Memory`, `planning.NewMemory()`
- `planning.Plan`, `planning.Item`, `planning.Status` (`Pending`, `InProgress`, `Completed`, `Cancelled`)
- `planning.Instructions`
- Tool names: `write_plan`, `read_plan`, `add_task`, `update_task_status`, `remove_task`

## Gotchas

- The toolset serializes its own load-mutate-save steps, but two toolsets
  sharing one store and key are separate writers. Scope keys per run or
  tenant, or serialize in the store.
- History compaction can drop earlier plan results. The plan is still in
  the store; the model can call `read_plan` to restore it, but the
  package does not re-inject it automatically before each request.
- `write_plan` replaces the whole plan, including IDs. Use `add_task`
  and `update_task_status` to preserve progress.
- A plan is prompt content, not a security boundary. Do not store
  secrets in step text, and re-check high-stakes actions in the tool
  that performs them.
- The deciding design record is [ADR 0044](../adr/0044-planning-tools.md).
