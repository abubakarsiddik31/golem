# Orchestration

## Purpose

Compose agents into multi-step and multi-agent workflows in plain Go —
fan-out, pipeline, router — with no graph engine and no framework
between you and the language.

## When to use

When a task needs more than one agent: specialists run side by side,
one agent's typed output feeding the next, or a route decided before a
specialist runs. When the *model* should decide whether to delegate a
subtask, use [agent delegation](agent-delegation.md) instead — that is
the model-driven form of the same idea. A graph/workflow engine stays
out of Golem by design; Go's goroutines, channels, and typed function
composition are the orchestration layer, and this guide is the recipe
book for the patterns that work.

## How it works

Every pattern below is application code over two guarantees Golem
already makes:

- **A shared agent serves concurrent runs.** Runs carry their own
  identity, events, and conversations; nothing on the agent mutates
  during a run. One `*golem.Agent` can be a field on your service and
  fire in a hundred goroutines.
- **A run's history is immutable to its caller.** `Result.Messages`
  belongs to the caller; a later run never mutates it. Piping one
  result into another agent is safe by construction.

What the application owns is exactly what patterns.md rule 6 says:
the goroutines, their owner, the cancellation path, the completion
signal, and error delivery.

### Fan-out, fan-in

Run specialists concurrently on the same input and merge their typed
answers. One goroutine per agent, one error slot per agent, no shared
state:

```go
func fanOut(ctx context.Context, prompt string, agents ...*golem.Agent[struct{}, string]) ([]string, error) {
	answers := make([]string, len(agents))
	errs := make([]error, len(agents))
	var wg sync.WaitGroup
	for i, agent := range agents {
		wg.Add(1)
		go func(i int, agent *golem.Agent[struct{}, string]) {
			defer wg.Done()
			result, err := agent.Run(ctx, golem.RunContext[struct{}]{}, prompt)
			if err != nil {
				errs[i] = err
				return
			}
			answers[i] = result.Output
		}(i, agent)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return answers, nil
}
```

Cancellation is inherited: cancelling `ctx` ends every in-flight run
at its next boundary, and each failure surfaces in its slot. Each
agent can have a different `Output` type — the merge is yours, which
is the point.

### Pipeline

One agent's typed output becomes the next agent's input — sequential,
so no concurrency at all:

```go
extracted, err := extractor.Run(ctx, runCtx, document)
if err != nil {
	return err
}
reviewed, err := reviewer.Run(ctx, runCtx,
	fmt.Sprintf("Review this extraction:\n%s", extracted.Output))
```

With [structured output](structured-output.md) the hand-off is typed:
the first agent decodes to a struct, the second's prompt renders it.
With [sessions](sessions.md), give each stage its own conversation ID
when the stages' transcripts must be inspectable separately, or one
shared ID when they form one logical conversation.

### Router

Decide which specialist runs *before* paying for it: a cheap
classification call picks the agent, the specialist does the work.

```go
route, err := classifier.Run(ctx, runCtx, question) // "billing" | "tech" | "other"
if err != nil {
	return err
}
switch strings.TrimSpace(route.Output) {
case "billing":
	return billing.Run(ctx, runCtx, question)
case "tech":
	return tech.Run(ctx, runCtx, question)
default:
	return general.Run(ctx, runCtx, question)
}
```

When the model should decide *mid-conversation* whether to delegate —
with the subtask phrased by the model itself — that is agent
delegation: the specialist is a tool, and the loop calls it when the
plan says to. The two compose: a delegation tool's sub-agent can
itself be the head of a pipeline.

### Bounded map

Batch over many inputs with a semaphore, not unbounded goroutines:

```go
sem := make(chan struct{}, 4) // four concurrent runs
var wg sync.WaitGroup
for _, input := range inputs {
	wg.Add(1)
	go func(input string) {
		defer wg.Done()
		sem <- struct{}{}
		defer func() { <-sem }()
		result, err := agent.Run(ctx, runCtx, input)
		// collect result/err under your own locking
	}(input)
}
wg.Wait()
```

Providers rate-limit; the bound is where your account's concurrency
policy lives, in your code, visible in review.

## Example

Run `examples/orchestration` — offline: two specialists run
concurrently on one prompt, and their outputs feed a synthesizer's
prompt. `go test ./...` exercises nothing here because there is
nothing to unit-test in Golem; the example is the executable spec.

## API surface

None. This guide uses `golem.New`, `Agent.Run`, `DecodeFunc`,
`WithInstructionsFunc`, and `AsTool` — see their guides. The patterns
are application code, which is the claim: if composition needs a Golem
type, the type is missing; so far it has not.

## Gotchas

- Goroutines in the examples belong to the caller. Golem never starts
  one for orchestration; the core's rule against undocumented
  background goroutines is why these patterns live in your code.
- `Run` is per-goroutine-safe, not per-agent-stateful: two concurrent
  runs of one agent each get their own conversation. Sharing one
  *conversation* across goroutines is a sessions question — the store
  serializes nothing, so runs of one conversation belong to one
  request path (see the sessions gotchas).
- Do not share a `testmodel.Scripted` (or any non-thread-safe model)
  between concurrently running agents — that is a property of the
  fake, not of the agent.
- Aggregating N specialist answers costs N model calls. If the
  specialists only differ by instruction, one agent with the context
  of all N inputs is usually cheaper; fan-out earns its cost when the
  specialists need different models, different tools, or isolation
  from each other's context windows.
- A router's `switch` on model output is program logic — treat the
  route as untrusted input, normalize it, and have a default. For
  schema-shaped routing, structured output with an enum beats parsing
  prose.
- The decision that a graph engine stays out — and what would reopen
  it — is in the [roadmap](../ROADMAP.md) and the foundation brief's
  non-goals.
