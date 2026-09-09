# Testing without a provider

## Purpose

Test agent behavior — requests, tool execution, evidence, error stages —
deterministically, with no network, credentials, or clocks.

## When to use

Always, for unit and contract tests. Live-provider checks are opt-in
integration tests that skip without environment keys.

## How it works

`testmodel.Scripted` plays queued `model.Response` values or errors and
records the normalized requests an agent sends. Use it when a fixed
conversation is enough; use `testmodel.Func` when the response should
inspect the request, and `testmodel.StreamFunc` for streaming contracts.
All three are provider-free and deterministic. Drive the agent, then assert
three things:

1. **The exact normalized request** — ordered messages, tool specs,
   output schema, and that the caller's `context.Context` reached the
   model.
2. **Ordered result evidence** — `Result.Messages` in execution order,
   including rejected responses and tool exchanges.
3. **Error identity** — the failing stage via `errors.As(*RunError)` and
   the original cause via `errors.Is`/`errors.As`.

Cover the primary failure path, not only success. Avoid assertions that
need sleeps or scheduling luck.

### Fuzzing the durable contracts

The message-JSON shape is the v1 durability promise, so it carries a Go
fuzz target: `model.FuzzMessageJSONIsDurable` feeds arbitrary bytes
through decode → marshal → decode and pins two properties — anything
Golem marshals must decode again, and the second marshaling must be
byte-identical to the first (persisting what you read never rewrites
meaning). Its seed corpus runs inside the normal `go test ./...`, so
every PR exercises it; continuous fuzzing runs bounded (`-fuzztime 90s`)
in a CI job on pushes to main, and any crasher lands in
`model/testdata/fuzz/` as a regression entry every later run replays.
To fuzz locally: `go test ./model -fuzz FuzzMessageJSONIsDurable -fuzztime 30s`.

### Live smoke matrix

The adapters' live tests (`TestLive*` in `providers/*/integration_test.go`)
skip without credentials. `scripts/smoke.sh` runs the matrix: it checks
each adapter's `GOLEM_*` keys, runs the tests where credentials exist,
prints which adapters ran and which skipped, and exits nonzero only when
a run test fails — an all-skip run is a clean pass. `scripts/smoke.sh
openai bedrock` filters to named adapters.

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

- `testmodel.New()` — queues outcomes with `Respond` and `Fail`, and returns a
  scripted `model.StreamingModel`; `Requests` returns a snapshot of what it
  received.
- `testmodel.Func` and `testmodel.StreamFunc` — function adapters for tests
  whose outcome depends on the normalized request or context.
- `testmodel.Emit` — safely forwards a streaming delta from a `StreamFunc`.
- `golem.DecodeFunc[Output]` — inline decoders for assertions.

## Gotchas

- Never make core unit tests depend on a live model, network, clock, or
  environment variable; adapter integration tests stay in the adapter
  package and skip without keys.
- CI runs every program in `examples/` and requires exit 0: keyed
  examples must print instructions and exit when their API key is
  unset, scripted ones must run their scenario — that house rule is
  enforced, not aspirational.
- For cancellation paths, cancel the context before the call and assert
  the error identity — no timers.
- The contract test matrix lives in
  `.agents/skills/golem-contract-tests/references/test-matrix.md`.
