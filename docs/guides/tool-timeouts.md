# Tool timeouts

## Purpose

Bound one tool execution so an unavailable dependency does not hold an agent
run indefinitely.

## When to use

Use a default timeout for tools that call a network or service, and a shorter
per-tool timeout for operations with a tighter service-level objective.

## How it works

`golem.WithToolTimeout` supplies the default deadline. A non-zero
`tool.Tool.Timeout` overrides it for that tool. Golem derives a context with
that deadline and passes it to `Tool.Exec`; tool authors must honor
`ctx.Done()`, close their own resources, and return the context error. The
framework does not start a background goroutine to abandon a tool that ignores
cancellation.

An expired tool deadline ends the run with `context.DeadlineExceeded`, left
unwrapped so callers can use `errors.Is`.

## Example

```go
lookup := tool.MustNew(tool.Tool[Deps]{
    Name: "lookup",
    Schema: json.RawMessage(`{"type":"object"}`),
    Timeout: 2 * time.Second,
    Exec: func(ctx context.Context, deps Deps, args json.RawMessage) (string, error) {
        return service.Lookup(ctx, args)
    },
})

agent, err := golem.New[Deps, string](model, decoder,
    golem.WithToolTimeout[Deps, string](5*time.Second),
    golem.WithTools[Deps, string](lookup),
)
```

## API surface

- `golem.WithToolTimeout[Deps, Output](time.Duration)`
- `tool.Tool.Timeout time.Duration`

## Gotchas

- A timeout cannot safely stop a tool that ignores its context; always pass the
  supplied context to outbound calls.
- Zero disables that timeout level; negative values are rejected at agent or
  tool construction.
