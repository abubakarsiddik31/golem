# ADR 0038: Run boundary events and a tracing port

## Status

Accepted. First item of the pre-freeze observability work.

## Context

Golem's execution evidence rule (foundation principle 4) says a run
result retains messages, tool activity, usage, and terminal cause
without forcing a tracing backend. The event stream delivers that
evidence while the run executes, and the guides name "tracing spans"
as a use the observer covers. In practice, though, three things were
missing for a real tracer.

First, the event stream had no run boundary. `model_start` and
`tool_start` arrive mid-run, but nothing marks where a run begins or
ends, so a span tree has no root: an application could open spans for
attempts and tools but had to invent its own run framing.

Second, a decoder correction round is invisible at the runner level.
The agent rejects an output and calls the loop again; at the event
level the run simply continues. A tracer that roots a span per run
needs to know these are two passes of one run.

Third, bridging to OpenTelemetry means importing its SDK, and this
module's rule is zero dependencies. An example cannot even compile
against the SDK without breaking that rule, because CI builds every
example. Pydantic AI ships first-party OpenTelemetry instrumentation;
that route is closed to Golem by design, so the parity gap had to be
closed the way the module closes every provider gap: with a port the
application implements, not a dependency it must take.

## Decision

Two additions, one ADR, because the second is useless without the
first.

**Run boundary events.** `EventRunStart` and `EventRunEnd` bracket one
pass of the execution loop. `run_start` fires after configuration is
validated and before the first model turn; `run_end` fires on every
terminal path — success, provider failure, cancellation, loop
exhaustion, and the deferred pause — carrying the pass's accumulated
usage and the terminal error. A run that fails validation before the
loop emits nothing. Because the agent drives one loop pass per
correction round, a corrected run shows two bracket pairs under one
`RunID`, with `EventOutputRejected` — which the agent emits between
passes — arriving between them. That is the honest shape of the
machine: the runner never knows the decoder's verdict, so the runner
cannot bracket the agent's whole run.

**A tracing port.** `package telemetry` defines `Tracer` and `Span` in
OpenTelemetry's shape — `Start(ctx, name, opts...) (ctx, span)`,
`SetAttributes`, `RecordError`, `End` — with `SpanKind` values for
internal and client work. `telemetry.Observer(tracer)` adapts the port
to the event stream: one root span per pass, a client span per
provider attempt, an internal span per tool execution, attributed with
run and conversation IDs, turn and attempt numbers, usage counters,
tool arguments and results, and decoder rejections. Failures are
recorded on the span they failed. Spans parent through the context
`Start` returns, exactly as OpenTelemetry's do, so an adapter forwards
both and the SDK's span context does the parenting. The adapter owns
the clock: spans run from `Start` to `End`, so no timestamps travel
through the event stream.

Attribute keys are exported constants with a `golem.` prefix, stable
across releases. Tool arguments and results are recorded verbatim,
like every other observer of the event stream sees them; redaction, if
needed, belongs to the adapter, which is where the application's
policy lives.

Cross-pass bookkeeping is deliberately bounded. A rejection arrives
when nothing is open, and the loop deterministically starts the next
pass, so the observer holds it in a pending entry that the next pass
consumes. Nothing is retained per run after the run ends, so a shared
agent serving unbounded runs holds no unbounded state.

## Alternatives considered

**Import `go.opentelemetry.io/otel` and ship real spans.** The
obvious parity move and the wrong one here: protobuf and gRPC
transitively, the largest dependency tree in the Go ecosystem, in a
module whose table stakes are stdlib only. It would also decide the
exporter's resource and propagation policy for every user, which is
application configuration.

**Document a recipe instead of a port.** The event stream plus a
guide section would technically suffice — an application can write the
observer itself. But the span tree is subtle exactly where it is
invisible: the correction-round bracketing, the parallel tool barrier
(starts before the group, ends after, so no stack), out-of-bracket
events, and usage attribution. Every application would rediscover
those rules; the observer centralizes them, and its tests are the
record.

**Root one span per agent run, not per loop pass.** The agent could
bracket its whole run — corrections included — at the `runLoop` level.
Then the runner's own passes would nest inside unmarkedly, and a bare
`runner.Execute` caller would have no boundary events at all. The
runner is the canonical loop; the boundary belongs to it.

**Timestamps on events.** `Event` carrying `time.Time` would let any
consumer reconstruct durations. The observer needs no them — the
adapter's SDK times spans between `Start` and `End` — and wall-clock
fields on a hot, synchronous path invite consumers to depend on
timing guarantees the event contract does not make.

**Span status as a port concept.** OpenTelemetry separates
`RecordError` from `SetStatus`. For this surface, one method is the
whole contract: an adapter that records an error sets an error status.
Two methods would ask every adapter author a policy question the
observer has already answered.

## Consequences

An application adds tracing in two lines — `WithRunEvents(telemetry.Observer(myTracer))` —
and writes its OpenTelemetry adapter once, a few dozen lines the
guide shows in full. The adapter, the SDK, the exporter, and the
resource stay application-side, upgradeable without touching Golem.

Trace shapes now follow the machine: one root per loop pass, not one
per agent-level `Run`. Dashboards that count runs by root-span count
overcount exactly when outputs need correction; they should count
distinct `golem.run.id`. The guide says so.

The event stream gained two kinds, which is additive: observers that
switch on kinds they know and ignore the rest keep working, and the
ordering contract gained its first guaranteed first and last events.

If a future need justifies a first-party OpenTelemetry bridge, it is
additive: a package that adapts this port to the SDK. The port, not
the SDK, is the seam — that was the point of deciding it first.
