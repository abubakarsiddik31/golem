// Package telemetry turns a run's event stream into spans through a
// small port an OpenTelemetry SDK can implement. Golem owns the span
// tree — one root per run bracket, a span per provider call attempt,
// a span per tool execution, with attributes and error recording — and
// the application owns the exporter: an adapter of a few dozen lines
// forwards the port to go.opentelemetry.io/otel, which stays out of
// this module's dependency graph entirely.
//
// The port follows the module's rule for optional capability: an
// interface the application implements, never a dependency it must
// take. See docs/adr/0038-run-events-tracing-port.md.
package telemetry

import "context"

// Tracer starts spans, the way an OpenTelemetry tracer does. The
// returned context carries the new span; a Start called with it
// produces a child.
//
// An adapter typically forwards to an SDK tracer: name becomes the
// span name, options carry kind and starting attributes, and the
// returned context wraps the SDK's span context so parenting survives.
type Tracer interface {
	Start(ctx context.Context, name string, opts ...SpanOption) (context.Context, Span)
}

// Span is one timed operation. An adapter owns the clock: the span
// runs from Start to End, so a tracing backend reports real durations
// without Golem carrying timestamps through the event stream.
//
// End may be called at most once; the observer calls it exactly once
// per span it opens. SetAttributes and RecordError must be called
// before End to land on the span.
type Span interface {
	// SetAttributes records key-value pairs on the span.
	SetAttributes(attrs ...Attribute)
	// RecordError marks the span as failed and attaches err.
	RecordError(err error)
	// End closes the span.
	End()
}

// SpanKind classifies a span the way OpenTelemetry does, so an adapter
// can set the SDK's kind verbatim.
type SpanKind int

const (
	// SpanKindInternal is work inside the agent: the run itself and
	// tool executions.
	SpanKindInternal SpanKind = iota
	// SpanKindClient is an outgoing call: the provider request.
	SpanKindClient
)

func (k SpanKind) String() string {
	switch k {
	case SpanKindInternal:
		return "internal"
	case SpanKindClient:
		return "client"
	default:
		return "unknown"
	}
}

// SpanOption configures one span at Start. An adapter applies the
// options to a SpanConfig and reads it — the zero SpanConfig is the
// default: SpanKindInternal with no attributes.
type SpanOption func(*SpanConfig)

// SpanConfig carries a span's starting settings.
type SpanConfig struct {
	// Kind classifies the span. The zero value is SpanKindInternal.
	Kind SpanKind
	// Attributes are recorded when the span opens, before any work
	// runs.
	Attributes []Attribute
}

// WithSpanKind sets the span's kind. The default is SpanKindInternal.
func WithSpanKind(kind SpanKind) SpanOption {
	return func(c *SpanConfig) {
		c.Kind = kind
	}
}

// WithAttributes sets attributes recorded when the span opens, before
// any work runs.
func WithAttributes(attrs ...Attribute) SpanOption {
	return func(c *SpanConfig) {
		c.Attributes = append(c.Attributes, attrs...)
	}
}

// Attribute is one key-value pair on a span. String, Int, and Bool
// construct every attribute this package defines; an adapter that
// handles those three value types handles them all.
type Attribute struct {
	Key   string
	Value any
}

// String returns a string-valued attribute.
func String(key, value string) Attribute {
	return Attribute{Key: key, Value: value}
}

// Int returns an int-valued attribute.
func Int(key string, value int) Attribute {
	return Attribute{Key: key, Value: value}
}

// Bool returns a bool-valued attribute.
func Bool(key string, value bool) Attribute {
	return Attribute{Key: key, Value: value}
}
