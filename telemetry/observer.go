package telemetry

import (
	"context"
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
	// document-sized output; see the guide's gotchas.
	AttrToolArgs   = "golem.tool.args"
	AttrToolResult = "golem.tool.result"
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
// results. Pass the result to WithRunEvents or WithRunObserver.
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
	o := &observer{tracer: tracer, runs: map[string]*runTrace{}, pending: map[string]int{}}
	return o.observe
}

type observer struct {
	mu     sync.Mutex
	tracer Tracer
	runs   map[string]*runTrace
	// pending holds decoder rejections observed between two passes of
	// one RunID. Each entry is created by a rejection and consumed by
	// the immediately following pass, so entries do not accumulate.
	pending map[string]int
}

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
		run.span.RecordError(event.Err)
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
		span.RecordError(event.Err)
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
		attrs = append(attrs, String(AttrToolArgs, string(event.Args)))
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
		span.SetAttributes(String(AttrToolResult, event.Result))
	}
	if event.Err != nil {
		span.RecordError(event.Err)
	}
	span.End()
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
