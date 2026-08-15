# ADR 0008: Streaming port

## Status

Accepted.

## Context

Every Golem run currently returns only after the model's full response
arrives. Real applications need fragments as they are generated: chat
UIs render tokens as they arrive, and long generations otherwise look
like hangs. Streaming is the last major capability missing from the core,
and ADR 0003 anticipated it: "streaming support will require per-adapter
SSE handling under these same rules, with its own ADR."

Two constraints shape the design. First, `model.Model` must not grow a
method: every implementor — test fakes, adapters, application mocks —
would break, and not every provider can stream at all. Second, the
repository forbids goroutines without a documented lifecycle, which rules
out the usual channel-and-pump designs.

This decision covers the port and the adapter layer only. Agent-level
streaming runs — how `RunStream` interleaves fragments with tool
execution and output decoding — get their own ADR, exactly as ADR 0006
deferred tool self-correction.

## Decision

### Streaming is an optional capability interface

```go
type StreamingModel interface {
    Model
    GenerateStream(ctx context.Context, request Request, onDelta func(Delta) error) (Response, error)
}
```

This is Go's `http.Pusher` idiom: implementations that can stream
advertise it; the rest are unaffected. Fakes in tests keep implementing
`Generate` alone. Nothing about `Model` changes.

### Delivery is a synchronous callback, not a channel or iterator

`onDelta` fires once per fragment, on the goroutine that called
`GenerateStream`, while the response is being read. There are no
goroutines, no buffers, no backpressure policy to specify — the
no-goroutines rule is satisfied structurally. Cancellation has two
sides, both explicit:

- **Caller side**: `onDelta` returning a non-nil error stops the stream,
  and `GenerateStream` returns that error as-is. Stopping is the
  caller's decision, not a provider failure, so it is not wrapped.
- **Transport side**: `ctx` cancellation aborts the underlying response
  body, surfacing as the adapter's normal transport error — the same
  contract as `Generate`.

### Deltas are fragments, not snapshots

```go
type Delta struct {
    Content   string
    ToolCalls []ToolCallDelta
}
type ToolCallDelta struct {
    Index       int
    ID, Name    string
    ArgsFragment string
}
```

A delta is the provider-honest unit — a text fragment or tool-call
argument piece, with `Index` correlating fragments to calls and `ID`/
`Name` present only on a call's first fragment. Deltas are in-flight
progress, not a persistence contract: unlike `Message` (ADR 0005), they
carry no JSON tags and no stability promise.

### Accumulation is adapter-owned; the assembled response is canonical

Merging indexed fragments is wire-format-specific, so it lives in the
adapter, and `GenerateStream` returns the fully assembled
`model.Response` — exactly what `Generate` would have returned, under
the same ADR 0003 normalization (first choice wins, stringified
arguments become raw JSON with empty mapped to `{}`, usage zeroes when
absent). The consequence is deliberate: the assembled response stays
the single canonical artifact for evidence, tool execution, and
decoding, and a future agent streaming loop can consume it unchanged.

### SSE rules

The OpenAI-compatible adapter streams with `stream: true` and requests
`stream_options: {"include_usage": true}`. Parsing follows the
chat-completions SSE shape: `data: {...}` lines carrying chunks, a final
`data: [DONE]` sentinel, comments (`: keep-alive`) and unknown lines
ignored. The sentinel is **required**: EOF without it means the stream
was truncated, and truncation returns a typed decode error — never
silent success with partial content. Usage arrives in a final chunk
with empty choices; providers that do not report it yield zeroes.
Argument fragments can exceed default line budgets, so the scanner
allowance is sized for it (1 MB).

## Alternatives considered

- **Adding `GenerateStream` to `Model`.** Rejected: breaks every
  implementor and forces streaming where providers cannot.
- **Channels or `iter.Seq`.** Rejected: a pump goroutine needs a
  lifecycle, cancellation, and backpressure story for zero benefit; a
  synchronous callback has none of those questions.
- **Message-so-far snapshots instead of fragments.** Rejected: quadratic
  re-sending, and not the unit providers actually emit.
- **A port-level accumulator shared by adapters.** Rejected: premature
  sharing per ADR 0003's two-adapter rule; index-merge semantics belong
  to each wire format.
- **Tolerating EOF without `[DONE]`.** Rejected: a proxy or provider
  cutting the stream would then look like a successful short answer —
  the exact failure streaming must not hide.

## Consequences

- Adapters opt in by implementing one method; capability detection is a
  type assertion at the caller.
- Evidence, usage, and error semantics are unchanged: the assembled
  response is canonical, deltas are advisory progress.
- Delta consumers must treat fragments as transient; nothing durable
  depends on them.
- Agent-level streaming runs remain to be designed (their own ADR) and
  can build on this port without revisiting it.
