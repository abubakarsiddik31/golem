# Tracing

## Purpose

Turn a run's event stream into spans — one root per pass through the
execution loop, a span per provider call attempt, a span per tool
execution — through a port an OpenTelemetry SDK implements, with no
dependency on Golem's side.

## When to use

When runs land in a tracing backend: Jaeger, Tempo, Honeycomb, or a
vendor SDK, wired through `go.opentelemetry.io/otel`. When post-hoc
inspection is enough, read `Result` and `RunError.Partial` instead;
when you only need to see a run as it happens without spans, a plain
`WithRunEvents` observer is lighter than this package. Golem ships no
SDK: the adapter, exporter, and resource are application code, so the
module stays dependency-free.

## How it works

`telemetry.Observer(tracer)` returns an observer for `WithRunEvents`
or `WithRunObserver`. It opens spans from the events the loop emits:

- `agent run` (internal) — one root per bracketed pass of the loop,
  opened at `run_start`, closed at `run_end` with the pass's usage and
  terminal error.
- `model request` (client) — one per provider call attempt, with the
  turn and attempt numbers and the attempt's usage; a failed attempt
  records its error.
- `tool <name>` (internal) — one per tool execution, with the call ID,
  the model's arguments, and the tool's text result; a failed execution
  records its error, and a call that deferred ends its span marked
  `golem.tool.deferred`.

Every span carries `golem.run.id` and, on the root, `golem.conversation.id`.
Attribute keys are exported constants (`telemetry.AttrRunID` and
friends), stable across releases.

A decoder correction round is a second bracketed pass under the same
`golem.run.id`: the rejected pass closes, the agent emits
`output_rejected` between passes, and the correction pass opens with
`golem.output.corrections` naming how many rejections preceded it.
Count runs by distinct `golem.run.id`, not by root-span count.

Parenting works the way OpenTelemetry's does: `Start` returns a
context carrying the new span, and the observer starts children from
it, so an adapter that forwards the context gets correct parenting
from the SDK's span context. The adapter owns the clock — a span lasts
from `Start` to `End` — so durations come from the SDK, not from the
event stream.

The observer holds one state entry per open run and deletes it at run
end; a rejection between passes is held only until the next pass
consumes it. A shared agent serving many concurrent runs is safe: the
observer synchronizes, and interleaved runs parent their spans to
their own roots.

## Example

Run `examples/tracing` — offline: the example implements the port with
an in-memory recorder that prints the finished tree.

```text
agent run  (19µs)
  golem.run.id = 01a171dc-…
  golem.conversation.id = 01a171dc-…
  golem.usage.input_tokens = 550
  model request  (1µs)
    golem.turn = 0
    golem.attempt = 1
  tool get_player_name  (2µs)
    golem.tool.name = get_player_name
    golem.tool.call.id = call-1
    golem.tool.result = Anne
  model request  (4µs)
```

The OpenTelemetry adapter is the whole integration — apply the
options, forward to an SDK tracer, wrap the SDK's span:

```go
type otelTracer struct{ tracer trace.Tracer }

func (t otelTracer) Start(ctx context.Context, name string, opts ...telemetry.SpanOption) (context.Context, telemetry.Span) {
    cfg := telemetry.SpanConfig{}
    for _, opt := range opts {
        opt(&cfg)
    }
    attrs := make([]attribute.KeyValue, 0, len(cfg.Attributes))
    for _, a := range cfg.Attributes {
        switch v := a.Value.(type) {
        case string:
            attrs = append(attrs, attribute.String(a.Key, v))
        case int:
            attrs = append(attrs, attribute.Int(a.Key, v))
        case bool:
            attrs = append(attrs, attribute.Bool(a.Key, v))
        }
    }
    ctx, span := t.tracer.Start(ctx, name,
        trace.WithSpanKind(trace.SpanKind(cfg.Kind)),
        trace.WithAttributes(attrs...))
    return ctx, otelSpan{span}
}

type otelSpan struct{ span trace.Span }

func (s otelSpan) SetAttributes(attrs ...telemetry.Attribute) {
    // Convert and forward, as in Start.
}
func (s otelSpan) RecordError(err error) {
    s.span.RecordError(err, trace.WithStackTrace(true))
    s.span.SetStatus(codes.Error, err.Error())
}
func (s otelSpan) End() { s.span.End() }
```

Then wire it once and every run is traced:

```go
agent, _ := golem.New[model, string](client, decoder,
    golem.WithTools[model, string](tools...),
    golem.WithRunEvents[model, string](telemetry.Observer(otelTracer{sdkTracer})),
)
```

## API surface

- `telemetry.Observer(tracer Tracer) runner.Observer` — the returned observer is a `func(golem.RunEvent)`; pass it to `golem.WithRunEvents` or `golem.WithRunObserver`
- `telemetry.Tracer` — `Start(ctx, name string, opts ...SpanOption) (context.Context, Span)`
- `telemetry.Span` — `SetAttributes(attrs ...Attribute)`, `RecordError(err error)`, `End()`
- `telemetry.SpanConfig{Kind, Attributes}` — what an adapter applies options to; the zero value is the default
- `telemetry.WithSpanKind(kind)`, `telemetry.WithAttributes(attrs ...Attribute)` — the `SpanOption` constructors
- `telemetry.SpanKind` — `SpanKindInternal`, `SpanKindClient`
- `telemetry.Attribute`, `telemetry.String`, `telemetry.Int`, `telemetry.Bool` — the attribute model and its constructors
- `telemetry.RunSpanName`, `telemetry.ModelSpanName`, `telemetry.AttrRunID`, `telemetry.AttrConversationID`, `telemetry.AttrTurn`, `telemetry.AttrAttempt`, `telemetry.AttrToolName`, `telemetry.AttrToolCallID`, `telemetry.AttrToolArgs`, `telemetry.AttrToolResult`, `telemetry.AttrToolDeferred`, `telemetry.AttrOutputCorrections`, `telemetry.AttrUsage*` — stable span and attribute names

## Gotchas

- Tool arguments and results are recorded **verbatim** on spans — the
  same bytes the event stream already exposed. If tools handle secrets
  or personal data, redact in the adapter (or skip this observer for
  those runs); a tracing backend is not a secret store.
- Two root spans for one logical run means correction happened. Group
  by `golem.run.id` for run counts; the roots' usage figures do not
  overlap — each pass reports its own.
- A run that ends by cancellation, loop exhaustion, or provider
  failure still closes its root span, with the error recorded on it. A
  deferred pause closes the root with no error: the run paused, it did
  not fail — `Result.Pending` is the evidence, not the span status.
- The observer is synchronous. `Start` and `End` run inline with the
  run, so a tracer whose SDK calls block (network export inside `End`)
  stalls the run — batch and export asynchronously, as SDKs do by
  default.
- Events for a `RunID` with no open bracket are dropped. That cannot
  happen from the loop; if you feed the observer a hand-built stream,
  start with `run_start`.
- The decision — boundary events at the loop, the port instead of an
  SDK dependency, and why tool output lands on spans — is ADR 0038.
