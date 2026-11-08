package telemetry

import (
	"context"
	"fmt"
	"sync"

	"github.com/abubakarsiddik31/golem/internal/runner"
	"github.com/abubakarsiddik31/golem/model"
)

// Attribute keys the observer sets. They are stable names an adapter
// or a query can rely on; values are strings, ints, or bools as the
// Attribute constructors produce them.
const (
	// AttrRunID and AttrConversationID identify the run and the
	// conversation it continues; on every span of the run.
	AttrRunID          = "golem.run.id"
	AttrConversationID = "golem.conversation.id"
	// AttrTurn and AttrAttempt locate one provider call attempt.
	AttrTurn    = "golem.turn"
	AttrAttempt = "golem.attempt"
	// AttrToolName and AttrToolCallID identify one tool execution.
	AttrToolName   = "golem.tool.name"
	AttrToolCallID = "golem.tool.call.id"
	// AttrToolArgs and AttrToolResult carry the model's arguments and
	// the tool's text result verbatim. They can hold prompt data and
	// document-sized output; see the guide's gotchas. With
	// WithContentExcluded they are absent and the size attributes
	// below carry their lengths instead.
	AttrToolArgs   = "golem.tool.args"
	AttrToolResult = "golem.tool.result"
	// AttrToolArgsSize and AttrToolResultSize record the byte size of
	// the arguments and the result, recorded in their place when the
	// observer runs with WithContentExcluded.
	AttrToolArgsSize   = "golem.tool.args_size"
	AttrToolResultSize = "golem.tool.result_size"
	// AttrToolDeferred marks a tool span whose call deferred instead of
	// executing; the run paused with it pending.
	AttrToolDeferred = "golem.tool.deferred"
	// AttrOutputCorrections counts decoder rejections the run has
	// incurred, recorded on the root span of the pass that follows
	// them: a rejected output ends one pass, the correction round is
	// the next pass under the same AttrRunID, and its root names how
	// many rejections preceded it.
	AttrOutputCorrections = "golem.output.corrections"

	AttrUsageInputTokens      = "golem.usage.input_tokens"
	AttrUsageOutputTokens     = "golem.usage.output_tokens"
	AttrUsageCacheReadTokens  = "golem.usage.cache_read_tokens"
	AttrUsageCacheWriteTokens = "golem.usage.cache_write_tokens"
	AttrUsageReasoningTokens  = "golem.usage.reasoning_tokens"
)

// Span names the observer assigns.
const (
	RunSpanName   = "agent run"
	ModelSpanName = "model request"
	// Tool spans are named "tool <name>"; see toolSpanName.
)

// Observer adapts a Tracer to the run event stream: it returns an
// observer that opens one root span per bracketed pass of the
// execution loop, a client span per provider call attempt, and an
// internal span per tool execution, each attributed with the run's
// identity and, where the event carries them, usage, arguments, and
// results. Content is recorded verbatim; ObserverWithOptions constrains
// it. Pass the result to WithRunEvents or WithRunObserver.
//
// Events outside a run bracket — a tool event whose RunID has no open
// run — are dropped: the bracket is the run's contract, and the loop
// never emits outside it. The returned observer is safe for
// concurrent runs; it keeps one state entry per open run and removes
// it at run end. A decoder rejection arrives between two passes, when
// nothing is open; the observer holds it in a pending entry that the
// next pass of the same RunID consumes — the loop always starts one —
// so nothing is retained beyond that pass. It never fails a run and
// never blocks beyond the tracer's own Start and End.
func Observer(tracer Tracer) runner.Observer {
	return ObserverWithOptions(tracer)
}

// ObserverWithOptions is Observer with observer options applied. The
// options constrain what content lands on the spans — see
// WithContentExcluded and WithRedactor; without them arguments,
// results, and error messages are recorded verbatim.
func ObserverWithOptions(tracer Tracer, opts ...ObserverOption) runner.Observer {
	config := observerConfig{}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt(&config)
	}
	o := &observer{tracer: tracer, config: config, runs: map[string]*runTrace{}, pending: map[string]int{}}
	return o.observe
}

// ObserverOption configures the observer Observer returns.
type ObserverOption func(*observerConfig)

type observerConfig struct {
	// excludeContent drops content-bearing attributes and records
	// sizes instead; errors are recorded by type only.
	excludeContent bool
	// redactor scrubs content-bearing attribute values and error
	// messages before they land on a span.
	redactor func(string) string
}

// WithContentExcluded returns an option that keeps content off the
// spans: tool arguments and results are replaced by their byte sizes,
// and every recorded error keeps only its type — error messages can
// quote the arguments, results, and refusals they belong to, so a
// content-excluding observer that forwarded them verbatim would leak
// the exclusion. The error chain survives: errors.As and errors.Is
// still reach the original through the wrapper. Exclusion wins when
// combined with WithRedactor.
func WithContentExcluded() ObserverOption {
	return func(c *observerConfig) {
		c.excludeContent = true
	}
}

// WithRedactor returns an option that scrubs content before it lands
// on a span: fn is applied to the tool arguments and result attribute
// values and to error messages. It does not touch identity attributes
// (run and conversation IDs, tool names) or usage counters. Combine
// with WithContentExcluded when nothing may leave the process; the
// exclusion then takes precedence and the redactor does not fire.
func WithRedactor(fn func(string) string) ObserverOption {
	return func(c *observerConfig) {
		c.redactor = fn
	}
}

type observer struct {
	mu     sync.Mutex
	tracer Tracer
	config observerConfig
	runs   map[string]*runTrace
	// pending holds decoder rejections observed between two passes of
	// one RunID. Each entry is created by a rejection and consumed by
	// the immediately following pass, so entries do not accumulate.
	pending map[string]int
}

// recordError marks the span failed under the configured content
// mode: verbatim by default, scrubbed by the redactor, or type-only
// under content exclusion.
func (o *observer) recordError(span Span, err error) {
	switch {
	case o.config.excludeContent:
		span.RecordError(&excludedError{err: err})
	case o.config.redactor != nil:
		span.RecordError(&redactedError{err: err, text: o.config.redactor(err.Error())})
	default:
		span.RecordError(err)
	}
}

// excludedError renders as the original error's type, so a span keeps
// the failure's shape without its message, and unwraps to the
// original for errors.As and errors.Is.
type excludedError struct {
	err error
}

func (e *excludedError) Error() string { return fmt.Sprintf("%T", e.err) }
func (e *excludedError) Unwrap() error { return e.err }

// redactedError renders as the redactor's output over the original
// error's message and unwraps to the original.
type redactedError struct {
	err  error
	text string
}

func (e *redactedError) Error() string { return e.text }
func (e *redactedError) Unwrap() error { return e.err }

// runTrace is one open bracketed pass: its root span and context, the
// model span currently in flight, tool spans by call ID, and the
// rejections that preceded this pass.
type runTrace struct {
	ctx         context.Context
	span        Span
	model       Span
	tools       map[string]Span
	corrections int
}

func (o *observer) observe(event runner.Event) {
	o.mu.Lock()
	defer o.mu.Unlock()

	switch event.Kind {
	case runner.EventRunStart:
		o.runStart(event)
	case runner.EventRunEnd:
		o.runEnd(event)
	case runner.EventModelStart:
		o.modelStart(event)
	case runner.EventModelEnd:
		o.modelEnd(event)
	case runner.EventToolStart:
		o.toolStart(event)
	case runner.EventToolEnd:
		o.toolEnd(event)
	case runner.EventDeferred:
		o.toolDeferred(event)
	case runner.EventOutputRejected:
		// The rejection lands between two passes, when the previous
		// bracket has closed and the next has not opened.
		o.pending[event.RunID]++
	case runner.EventCanceled:
		// The cancelling call's tool end already closed its span; the
		// run end records the sentinel.
	}
}

func (o *observer) runStart(event runner.Event) {
	run := &runTrace{tools: map[string]Span{}, corrections: o.pending[event.RunID]}
	delete(o.pending, event.RunID)
	o.runs[event.RunID] = run
	run.ctx, run.span = o.tracer.Start(context.Background(), RunSpanName,
		WithSpanKind(SpanKindInternal),
		WithAttributes(runAttributes(event)...))
}

func (o *observer) runEnd(event runner.Event) {
	run := o.runs[event.RunID]
	if run == nil {
		return
	}
	delete(o.runs, event.RunID)
	if run.span == nil {
		return
	}
	attrs := usageAttributes(event.Usage)
	if run.corrections > 0 {
		attrs = append(attrs, Int(AttrOutputCorrections, run.corrections))
	}
	run.span.SetAttributes(attrs...)
	if event.Err != nil {
		o.recordError(run.span, event.Err)
	}
	run.span.End()
}

func (o *observer) modelStart(event runner.Event) {
	run := o.runs[event.RunID]
	if run == nil || run.span == nil {
		return
	}
	if run.model != nil {
		// Defensive: an attempt that never reported its end still ends.
		run.model.End()
		run.model = nil
	}
	_, run.model = o.tracer.Start(run.ctx, ModelSpanName,
		WithSpanKind(SpanKindClient),
		WithAttributes(
			Int(AttrTurn, event.Turn),
			Int(AttrAttempt, event.Attempt)))
}

func (o *observer) modelEnd(event runner.Event) {
	run := o.runs[event.RunID]
	if run == nil || run.model == nil {
		return
	}
	span := run.model
	run.model = nil
	span.SetAttributes(usageAttributes(event.Usage)...)
	if event.Err != nil {
		o.recordError(span, event.Err)
	}
	span.End()
}

func (o *observer) toolStart(event runner.Event) {
	run := o.runs[event.RunID]
	if run == nil || run.span == nil {
		return
	}
	attrs := []Attribute{
		String(AttrToolName, event.ToolName),
		String(AttrToolCallID, event.CallID),
	}
	if len(event.Args) > 0 {
		if o.config.excludeContent {
			attrs = append(attrs, Int(AttrToolArgsSize, len(event.Args)))
		} else {
			attrs = append(attrs, o.contentAttribute(AttrToolArgs, string(event.Args)))
		}
	}
	_, span := o.tracer.Start(run.ctx, toolSpanName(event.ToolName),
		WithSpanKind(SpanKindInternal), WithAttributes(attrs...))
	run.tools[event.CallID] = span
}

func (o *observer) toolEnd(event runner.Event) {
	run := o.runs[event.RunID]
	if run == nil {
		return
	}
	span, ok := run.tools[event.CallID]
	if !ok {
		return
	}
	delete(run.tools, event.CallID)
	if event.Result != "" {
		if o.config.excludeContent {
			span.SetAttributes(Int(AttrToolResultSize, len(event.Result)))
		} else {
			span.SetAttributes(o.contentAttribute(AttrToolResult, event.Result))
		}
	}
	if event.Err != nil {
		o.recordError(span, event.Err)
	}
	span.End()
}

// contentAttribute renders one content-bearing attribute: scrubbed by
// the redactor when one is set, verbatim otherwise. Content exclusion
// is decided by the caller, which records a size instead.
func (o *observer) contentAttribute(key, content string) Attribute {
	if o.config.redactor != nil {
		content = o.config.redactor(content)
	}
	return String(key, content)
}

func (o *observer) toolDeferred(event runner.Event) {
	run := o.runs[event.RunID]
	if run == nil {
		return
	}
	span, ok := run.tools[event.CallID]
	if !ok {
		return
	}
	delete(run.tools, event.CallID)
	span.SetAttributes(Bool(AttrToolDeferred, true))
	span.End()
}

// runAttributes names the run: its identity.
func runAttributes(event runner.Event) []Attribute {
	attrs := []Attribute{String(AttrRunID, event.RunID)}
	if event.ConversationID != "" {
		attrs = append(attrs, String(AttrConversationID, event.ConversationID))
	}
	return attrs
}

// usageAttributes reports non-zero counters, so a span's attributes
// stay free of default zeros.
func usageAttributes(usage model.Usage) []Attribute {
	var attrs []Attribute
	if usage.InputTokens != 0 {
		attrs = append(attrs, Int(AttrUsageInputTokens, usage.InputTokens))
	}
	if usage.OutputTokens != 0 {
		attrs = append(attrs, Int(AttrUsageOutputTokens, usage.OutputTokens))
	}
	if usage.CacheReadTokens != 0 {
		attrs = append(attrs, Int(AttrUsageCacheReadTokens, usage.CacheReadTokens))
	}
	if usage.CacheWriteTokens != 0 {
		attrs = append(attrs, Int(AttrUsageCacheWriteTokens, usage.CacheWriteTokens))
	}
	if usage.ReasoningTokens != 0 {
		attrs = append(attrs, Int(AttrUsageReasoningTokens, usage.ReasoningTokens))
	}
	return attrs
}

func toolSpanName(tool string) string {
	return "tool " + tool
}
