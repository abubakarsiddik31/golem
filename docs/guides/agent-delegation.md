# Agent delegation

## Purpose

Run one agent as a tool of another, so a delegating agent can hand a
complete subtask to a specialist agent and use its typed result — the
Go-native route to multi-agent composition.

## When to use

Reach for delegation when one agent's responsibility is better owned by
another agent with its own instructions, tools, output type, and budgets:
a researcher agent consulted by a writer, a critic agent consulted by a
drafting agent. Do not reach for it when a plain tool suffices — a
function with a schema is simpler, cheaper, and easier to test. And do
not delegate merely to work around a crowded tool list; grouping tools
per agent is a design smell the model can usually handle on its own.

## How it works

`Agent.AsTool` returns an ordinary `tool.Tool`, so the sub-agent
registers with `WithTools` like any other tool and the existing run
loop, retry budgets, usage limits, and evidence rules apply unchanged.

The delegating model requests the tool with a `prompt` argument. The
sub-agent runs once per request with `Run`: its entire input is that
prompt — it sees nothing else of the delegating conversation — and its
`RunContext` carries the delegating run's dependency value, so both
agents must share the `Deps` type.

A successful run's typed output becomes the tool result as text: a
string output passes through unquoted, every other type is
JSON-encoded, and `WithAgentResult` replaces the rendering — and is the
hook for capturing the inner `Result`, whose messages and usage are not
otherwise part of the delegating run's evidence.

Activity counts stay separate too: the delegating run records the
delegation itself as one tool call, and the sub-agent's own requests
and tool executions appear only on the sub-agent's result — a run's
`Result.Requests` and `Result.ToolCalls` count that run alone.

Failures stay explicit. A missing or malformed `prompt` argument is
rejected with `*model.ModelRetry`, so the delegating run's tool retry
budget (`WithToolRetries`, or the tool's `MaxRetries`) governs
correction like any tool rejection. Every other sub-agent failure fails
the delegating run at the tool stage with the inner `RunError`
preserved in the chain, and cancellation propagates unwrapped. The
sub-agent's own usage limits, iteration bounds, and timeouts bound its
run; a tool timeout on the agent tool bounds the whole delegation.

## Example

The runnable program is `examples/delegation`:

```bash
OPENAI_API_KEY=sk-... go run ./examples/delegation
```

It wires a specialist agent as the planner's only tool:

```go
specialist, err := golem.New[config, string](client,
    golem.DecodeFunc[string](decodeContent),
    golem.WithInstructions[config, string]("You are a terse fact checker."))
research, err := specialist.AsTool("fact_checker",
    "Checks one factual claim and answers with the verdict.")
planner, err := golem.New[config, string](client,
    golem.DecodeFunc[string](decodeContent),
    golem.WithInstructions[config, string]("Delegate every claim before answering."),
    golem.WithTools[config, string](research),
)
result, err := planner.Run(ctx, golem.RunContext[config]{Deps: cfg},
    "Is the Eiffel Tower taller than the Golden Gate Bridge's towers?")
```

## API surface

- `(*Agent[Deps, Output]).AsTool(name, description string, options ...AgentToolOption[Deps, Output]) (tool.Tool[Deps], error)` — expose the agent as a tool.
- `golem.WithAgentResult[Deps, Output](fn func(ctx context.Context, output Output) (string, error)) AgentToolOption[Deps, Output]` — replace result rendering; capture the typed output or the inner evidence here.

## Gotchas

- The sub-agent's conversation is invisible to the delegating run and
  its result evidence: only the rendered text is recorded. Capture what
  you need in a `WithAgentResult` closure.
- Nothing stops an agent from delegating to itself, directly or in a
  cycle. Recursion is bounded only by iteration and usage limits; the
  schema prompts the model with one task per call, but the caller owns
  the delegation graph.
- Both agents share the `Deps` type. Mapping a delegating dependency
  value to a differently-typed sub-agent dependency is not built in;
  wrap `AsTool`'s tool in your own `tool.Tool` when the types must
  differ.
- An agent tool works with parallel tool calls — the sub-agent run is
  independent — but the delegating model sees only the rendered result,
  so keep each delegated task self-contained in its prompt.

Decisions live in `docs/adr/0013-agent-as-a-tool.md`.
