# Tools and dependencies

## Purpose

Give the model executable capabilities whose execution receives a typed,
application-owned dependency value.

## When to use

Whenever the answer depends on state the model cannot know — a database
handle, a tenant name, a logger — or an action the model cannot perform.

## How it works

A `tool.Tool[Deps]` is a typed value: inspectable metadata (name,
description, JSON Schema) plus an `Exec` function receiving the caller's
context, the run's `Deps` value, and raw model-produced arguments.
Golem executes requested calls sequentially and appends every exchange —
the assistant's tool-call message and each tool result — to the result's
`Messages` in execution order.

Arguments are untrusted model output and stay raw JSON: decoding and
validating them is the tool author's explicit job. Golem never uses
reflection to infer schemas or arguments.

## Example

Run `examples/tools`:

```bash
OPENAI_API_KEY=sk-... go run ./examples/tools
```

```go
type roster struct{ PlayerName string }

getPlayerName := tool.MustNew(tool.Tool[roster]{
    Name:        "get_player_name",
    Description: "Get the player's name.",
    Schema:      json.RawMessage(`{"type":"object"}`),
    Exec: func(ctx context.Context, deps roster, args json.RawMessage) (string, error) {
        return deps.PlayerName, nil
    },
})
agent, _ := golem.New[roster, string](client, decoder,
    golem.WithTools[roster, string](getPlayerName),
)
result, _ := agent.Run(ctx, golem.RunContext[roster]{Deps: roster{PlayerName: "Anne"}}, "who wins?")
```

## API surface

- `tool.New(tool.Tool[Deps]) (Tool[Deps], error)` and `tool.MustNew` — reject missing names, invalid schema JSON, nil Exec, and duplicate names at registration.
- `golem.WithTools[Deps, Output](tools ...tool.Tool[Deps])`
- `golem.WithParallelToolCalls[Deps, Output]()` enables concurrent calls in
  one model response; `tool.Tool.Sequential` makes one tool a barrier.
- `golem.WithToolChoice[Deps, Output](name)` advertises only the selected
  registered tool for runs where the application must constrain availability.
- `tool.Tool[Deps]{Name, Description, Schema, Exec}`

## Gotchas

- `Exec` must honor `ctx` and return errors rather than logging them.
- Tool failures abort the run at the `tool` stage unless they wrap
  `model.ModelRetry` (see [Self-correction](self-correction.md)).
- Tool metadata must be inspectable without executing the tool.
