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

### Parts and definitive failures

`Exec` returns a `tool.Result` — the result `Text` plus optional
`Parts` (`model.Part`, the same shape prompt parts use). Parts are
evidence the call produced: a screenshot, a fetched PDF. They are
stored on the tool message and ride the durable JSON, and each adapter
places them where its API carries them — inside the tool result on
Anthropic and Bedrock, framed onto one attributed user message on
OpenAI, Azure, and Gemini. An adapter that cannot carry a part kind
fails the run before any request; see the [multimodal
guide](multimodal-input.md) for each adapter's kind matrix.

A failure the model should see — the resource doesn't exist, the
operation isn't supported — is a `&tool.Failed{Reason}`, not a plain
error: the reason becomes the tool's result on a message flagged
`Failed`, the model decides what to do next, and the run continues
without consuming the tool's retry budget. Returning
`*model.ModelRetry` instead asks the model to correct the call and
consume budget; any other error still aborts the run at the `tool`
stage.

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
    Exec: func(ctx context.Context, deps roster, args json.RawMessage) (tool.Result, error) {
        return tool.Text(deps.PlayerName), nil
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
- `tool.Tool[Deps]{Name, Description, Schema, Exec}` — `Exec` returns
  `(tool.Result, error)`.
- `tool.Result{Text, Parts}` and `tool.Text(s)` — the result shape and
  the text-only helper.
- `&tool.Failed{Reason}` — a definitive failure recorded as the tool's
  result; the retry budget is untouched.

## Gotchas

- `Exec` must honor `ctx` and return errors rather than logging them.
- Tool failures abort the run at the `tool` stage unless they wrap
  `model.ModelRetry` (correction; see
  [Self-correction](self-correction.md)) or are a `*tool.Failed`
  (definitive; recorded as the result, budget untouched).
- Tool metadata must be inspectable without executing the tool.
