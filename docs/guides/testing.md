# Testing without a provider

## Purpose

Test agent behavior — requests, tool execution, evidence, error stages —
deterministically, with no network, credentials, or clocks.

## When to use

Always, for unit and contract tests. Live-provider checks are opt-in
integration tests that skip without environment keys.

## How it works

`model.Model` is one method; a fake is a struct with scripted responses
that records the requests it receives. Write the fake at the port your
code owns, drive the agent, and assert three things:

1. **The exact normalized request** — ordered messages, tool specs,
   output schema, and that the caller's `context.Context` reached the
   model.
2. **Ordered result evidence** — `Result.Messages` in execution order,
   including rejected responses and tool exchanges.
3. **Error identity** — the failing stage via `errors.As(*RunError)` and
   the original cause via `errors.Is`/`errors.As`.

Cover the primary failure path, not only success. Avoid assertions that
need sleeps or scheduling luck.

## Example

Run `examples/testing-without-a-provider` — a scripted model that
requests a tool once, then answers from the result:

```bash
go run ./examples/testing-without-a-provider
```

The repository's own suites are the reference: `fakeModel` and
`queuedModel` in the root tests, the `httptest` servers in the adapter
packages, and the opt-in `TestLive*` integration tests.

## API surface

- `model.Model` — implement `Generate(ctx, request) (Response, error)`; add `GenerateStream` only when testing streaming.
- `golem.DecodeFunc[Output]` — inline decoders for assertions.

## Gotchas

- Never make core unit tests depend on a live model, network, clock, or
  environment variable; adapter integration tests stay in the adapter
  package and skip without keys.
- For cancellation paths, cancel the context before the call and assert
  the error identity — no timers.
- The contract test matrix lives in
  `.agents/skills/golem-contract-tests/references/test-matrix.md`.
