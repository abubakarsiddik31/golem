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

### Cancelling the run from a tool

A tool that must end the whole run — a policy veto, an exhausted
budget, a stop gesture forwarded through the dependencies — returns
`&tool.Canceled{Reason}`. The run stops deliberately, not as a
failure: `Run` reports `RunError` with `StageCanceled` (match the
sentinel with `errors.As` to read the reason), no retry budget is
touched, and the evidence stays on `RunError.Partial`. Inside the
batch, execution order governs: calls before the cancelling call keep
their recorded results, the cancelling call and everything after it
are closed with a synthesized no-result result, and later calls never
start. The transcript is provider-valid immediately, so
`RunWithHistory` resumes it without repair — see [Resuming a failed
run](conversations-and-history.md#resuming-a-failed-run). A tool
cannot be force-stopped once running — the group it runs in finishes,
and only work that had not started is prevented. A delegated sub-agent
that cancels cancels the delegating run the same way.

### Per-step tool preparation

`WithPrepareTools` filters or rewrites the tool specs advertised to the
model before every request, including correction rounds. It is the
per-step counterpart to `WithTools`: registration stays static, while
advertisement can follow the caller's role, the step reached, or state
the tool calls left behind.

Preparation sees the specs *after* deferred-tool discovery has revealed
tools from history, so it can hide a revealed tool on a later step
without unregistering it. It may drop specs and rewrite a spec's
description or schema, but it cannot add a callable tool: every returned
name must already be advertised this turn, duplicate names are rejected,
and every schema must be valid JSON. When tool-mode output is active the
output tool must remain. A prepare error, or an invalid result, fails
the run before the request is sent.

### Output limits

Tool results persist in history, so an oversized result is re-sent on
every later request. `WithToolOutputLimit(maxBytes)` caps result text
once, when the tool returns: the model sees the head and tail plus a
marker naming the original size, and that reduced form is what persists
and what later requests re-send. Result parts are untouched.

```go
agent, _ := golem.New[struct{}, string](client, decoder,
    golem.WithTools[struct{}, string](readLog),
    golem.WithToolOutputLimit[struct{}, string](400),
)
```

A tool can set `tool.Tool.MaxOutputBytes` to override the agent cap;
zero inherits it, and an unset agent cap disables truncation. The cap is
bytes, determined once, and cuts on UTF-8 boundaries. It is truncation,
not spill-to-storage or summarization: a caller that needs the full
payload should keep it inside the tool or write it somewhere the model
can request intentionally.

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

`examples/tool-output-limit` runs offline and shows a 9 KB log reduced to
its head and tail:

```go
golem.WithToolOutputLimit[struct{}, string](400)
```

`examples/tool-preparation` runs offline and hides a destructive tool from
a non-admin caller:

```go
agent, _ := golem.New[actor, string](client, decoder,
    golem.WithTools[actor, string](readFile, deleteFile),
    golem.WithPrepareTools[actor, string](func(_ context.Context, runCtx golem.RunContext[actor], specs []model.ToolSpec) ([]model.ToolSpec, error) {
        if runCtx.Deps.Role == "admin" {
            return specs, nil
        }
        return keep(specs, "read_file"), nil
    }),
)
```

## API surface

- `tool.New(tool.Tool[Deps]) (Tool[Deps], error)` and `tool.MustNew` — reject missing names, invalid schema JSON, nil Exec, and duplicate names at registration.
- `golem.WithTools[Deps, Output](tools ...tool.Tool[Deps])`
- `golem.PrepareToolsFunc[Deps]` and `golem.WithPrepareTools[Deps, Output](fn)` — filter or rewrite the advertised specs per request
- `golem.WithToolOutputLimit[Deps, Output](maxBytes int)` — cap tool result text; `tool.Tool.MaxOutputBytes` overrides it
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
- `&tool.Canceled{Reason}` — a deliberate stop: the run ends at the
  cancellation stage with evidence on `RunError.Partial`.

## Gotchas

- `Exec` must honor `ctx` and return errors rather than logging them.
- Tool failures abort the run at the `tool` stage unless they wrap
  `model.ModelRetry` (correction; see
  [Self-correction](self-correction.md)), are a `*tool.Failed`
  (definitive; recorded as the result, budget untouched), or are a
  `*tool.Canceled` (deliberate stop; ends the run at the `canceled`
  stage).
- Tool metadata must be inspectable without executing the tool.
- Prepared specs cannot add a callable tool, duplicate a name, or drop the
  active output tool; the run fails before the request instead of sending
  a request the loop cannot dispatch. See
  `docs/adr/0045-per-step-tool-preparation.md`.
- The output cap keeps only text; a tool that returns an image or
  document part keeps the full part even when its text is truncated. See
  `docs/adr/0050-tool-output-limits.md`.
- Preparation runs after deferred-tool discovery, so removing the
  runner-owned `search_tools` spec disables discovery for that request;
  the deferred tools stay declared but unreachable.
- Any tool that can return `*tool.Canceled` holds the authority to end
  the run; read a tool returning it as part of that tool's contract.
