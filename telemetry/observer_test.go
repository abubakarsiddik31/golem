package telemetry_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/abubakarsiddik31/golem/internal/runner"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/telemetry"
	"github.com/abubakarsiddik31/golem/testmodel"
	"github.com/abubakarsiddik31/golem/tool"
)

// recorded is one span a test tracer saw.
type recorded struct {
	name   string
	kind   telemetry.SpanKind
	attrs  []telemetry.Attribute
	parent *recorded
	errs   []error
	ended  bool
}

type tracerKey struct{}

// fakeTracer is the smallest possible Tracer: it records spans and
// parents them through the context, the way an OpenTelemetry adapter
// parents through span context.
type fakeTracer struct {
	spans []*recorded
}

func (t *fakeTracer) Start(ctx context.Context, name string, opts ...telemetry.SpanOption) (context.Context, telemetry.Span) {
	cfg := telemetry.SpanConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	span := &recorded{name: name, kind: cfg.Kind, attrs: append([]telemetry.Attribute(nil), cfg.Attributes...)}
	if parent, ok := ctx.Value(tracerKey{}).(*recorded); ok {
		span.parent = parent
	}
	t.spans = append(t.spans, span)
	return context.WithValue(ctx, tracerKey{}, span), span
}

func (s *recorded) SetAttributes(attrs ...telemetry.Attribute) {
	s.attrs = append(s.attrs, attrs...)
}

func (s *recorded) RecordError(err error) {
	s.errs = append(s.errs, err)
}

func (s *recorded) End() {
	s.ended = true
}

// attr returns the one value set for key, and whether it was set.
func attr(s *recorded, key string) (any, bool) {
	var found any
	for _, a := range s.attrs {
		if a.Key == key {
			found = a.Value
		}
	}
	return found, found != nil
}

func intAttr(t *testing.T, s *recorded, key string) int {
	t.Helper()
	value, ok := attr(s, key)
	if !ok {
		t.Fatalf("span %q has no %s attribute; attrs = %v", s.name, key, s.attrs)
	}
	number, isInt := value.(int)
	if !isInt {
		t.Fatalf("span %q attribute %s = %v, want an int", s.name, key, value)
	}
	return number
}

func strAttr(t *testing.T, s *recorded, key string) string {
	t.Helper()
	value, ok := attr(s, key)
	if !ok {
		t.Fatalf("span %q has no %s attribute; attrs = %v", s.name, key, s.attrs)
	}
	text, isString := value.(string)
	if !isString {
		t.Fatalf("span %q attribute %s = %v, want a string", s.name, key, value)
	}
	return text
}

// spansNamed returns every started span with a name, in start order.
func (t *fakeTracer) spansNamed(name string) []*recorded {
	var out []*recorded
	for _, span := range t.spans {
		if span.name == name {
			out = append(out, span)
		}
	}
	return out
}

func TestObserverBuildsASpanTree(t *testing.T) {
	t.Parallel()

	tracer := &fakeTracer{}
	observe := telemetry.Observer(tracer)

	observe(runner.Event{Kind: runner.EventRunStart, RunID: "run-1", ConversationID: "con-1"})
	observe(runner.Event{Kind: runner.EventModelStart, RunID: "run-1", Turn: 0, Attempt: 1})
	observe(runner.Event{Kind: runner.EventModelEnd, RunID: "run-1", Turn: 0, Attempt: 1,
		Usage: model.Usage{InputTokens: 12, OutputTokens: 3}})
	observe(runner.Event{Kind: runner.EventToolStart, RunID: "run-1", Turn: 0, CallID: "c1",
		ToolName: "lookup", Args: json.RawMessage(`{"q":"golem"}`)})
	observe(runner.Event{Kind: runner.EventToolEnd, RunID: "run-1", Turn: 0, CallID: "c1",
		ToolName: "lookup", Result: "42"})

	// The run's root stays open while the run is in flight; the model
	// span legitimately ends before it, when its attempt completes.
	if root := tracer.spansNamed(telemetry.RunSpanName)[0]; root.ended {
		t.Fatal("root span ended before run end")
	}

	observe(runner.Event{Kind: runner.EventRunEnd, RunID: "run-1",
		Usage: model.Usage{InputTokens: 20, OutputTokens: 5}})

	roots := tracer.spansNamed(telemetry.RunSpanName)
	if len(roots) != 1 {
		t.Fatalf("roots = %d, want 1", len(roots))
	}
	root := roots[0]
	if root.kind != telemetry.SpanKindInternal || !root.ended {
		t.Fatalf("root = %+v, want an ended internal span", root)
	}
	if got := strAttr(t, root, telemetry.AttrRunID); got != "run-1" {
		t.Errorf("run id = %q", got)
	}
	if got := strAttr(t, root, telemetry.AttrConversationID); got != "con-1" {
		t.Errorf("conversation id = %q", got)
	}
	if _, ok := attr(root, telemetry.AttrOutputCorrections); ok {
		t.Error("an uncorrected run recorded corrections")
	}
	if got := intAttr(t, root, telemetry.AttrUsageInputTokens); got != 20 {
		t.Errorf("run input tokens = %d, want the run total", got)
	}
	if len(root.errs) != 0 {
		t.Errorf("run recorded errors = %v, want none", root.errs)
	}

	models := tracer.spansNamed(telemetry.ModelSpanName)
	if len(models) != 1 {
		t.Fatalf("model spans = %d, want 1", len(models))
	}
	attempt := models[0]
	if attempt.kind != telemetry.SpanKindClient {
		t.Errorf("attempt kind = %s, want client", attempt.kind)
	}
	if attempt.parent != root {
		t.Error("attempt is not a child of the run span")
	}
	if got := intAttr(t, attempt, telemetry.AttrTurn); got != 0 {
		t.Errorf("turn = %d", got)
	}
	if got := intAttr(t, attempt, telemetry.AttrAttempt); got != 1 {
		t.Errorf("attempt = %d", got)
	}
	if got := intAttr(t, attempt, telemetry.AttrUsageInputTokens); got != 12 {
		t.Errorf("attempt input tokens = %d", got)
	}
	if !attempt.ended {
		t.Error("attempt span did not end")
	}

	tools := tracer.spansNamed("tool lookup")
	if len(tools) != 1 {
		t.Fatalf("tool spans = %d, want 1", len(tools))
	}
	lookup := tools[0]
	if lookup.parent != root {
		t.Error("tool span is not a child of the run span")
	}
	if got := strAttr(t, lookup, telemetry.AttrToolName); got != "lookup" {
		t.Errorf("tool name = %q", got)
	}
	if got := strAttr(t, lookup, telemetry.AttrToolCallID); got != "c1" {
		t.Errorf("call id = %q", got)
	}
	if got := strAttr(t, lookup, telemetry.AttrToolArgs); got != `{"q":"golem"}` {
		t.Errorf("args = %q", got)
	}
	if got := strAttr(t, lookup, telemetry.AttrToolResult); got != "42" {
		t.Errorf("result = %q", got)
	}
	if !lookup.ended {
		t.Error("tool span did not end")
	}
}

func TestObserverRecordsFailures(t *testing.T) {
	t.Parallel()

	tracer := &fakeTracer{}
	observe := telemetry.Observer(tracer)

	attemptErr := errors.New("429: slow down")
	runErr := errors.New("run gave up")

	observe(runner.Event{Kind: runner.EventRunStart, RunID: "run-1"})
	observe(runner.Event{Kind: runner.EventModelStart, RunID: "run-1", Turn: 0, Attempt: 1})
	observe(runner.Event{Kind: runner.EventModelEnd, RunID: "run-1", Turn: 0, Attempt: 1, Err: attemptErr})
	observe(runner.Event{Kind: runner.EventRunEnd, RunID: "run-1", Err: runErr})

	attempt := tracer.spansNamed(telemetry.ModelSpanName)[0]
	if len(attempt.errs) != 1 || !errors.Is(attempt.errs[0], attemptErr) {
		t.Fatalf("attempt errors = %v, want the attempt failure", attempt.errs)
	}
	root := tracer.spansNamed(telemetry.RunSpanName)[0]
	if len(root.errs) != 1 || !errors.Is(root.errs[0], runErr) {
		t.Fatalf("run errors = %v, want the terminal failure", root.errs)
	}
}

func TestObserverHandlesDeferredAndCancellation(t *testing.T) {
	t.Parallel()

	tracer := &fakeTracer{}
	observe := telemetry.Observer(tracer)

	observe(runner.Event{Kind: runner.EventRunStart, RunID: "run-1"})
	observe(runner.Event{Kind: runner.EventModelStart, RunID: "run-1", Turn: 0, Attempt: 1})
	observe(runner.Event{Kind: runner.EventModelEnd, RunID: "run-1", Turn: 0, Attempt: 1})
	observe(runner.Event{Kind: runner.EventToolStart, RunID: "run-1", CallID: "c1", ToolName: "gated"})
	observe(runner.Event{Kind: runner.EventDeferred, RunID: "run-1", CallID: "c1", ToolName: "gated"})

	gated := tracer.spansNamed("tool gated")[0]
	if !gated.ended {
		t.Fatal("deferred call's span did not end")
	}
	if value, ok := attr(gated, telemetry.AttrToolDeferred); !ok || value != true {
		t.Fatalf("deferred attribute = %v, %v", value, ok)
	}

	observe(runner.Event{Kind: runner.EventRunEnd, RunID: "run-1"})

	// A cancelled run records the sentinel on its root.
	cancelled := &fakeTracer{}
	observeCancelled := telemetry.Observer(cancelled)
	sentinel := &tool.Canceled{Reason: "user said stop"}
	observeCancelled(runner.Event{Kind: runner.EventRunStart, RunID: "run-2"})
	observeCancelled(runner.Event{Kind: runner.EventToolStart, RunID: "run-2", CallID: "c1", ToolName: "stop"})
	observeCancelled(runner.Event{Kind: runner.EventToolEnd, RunID: "run-2", CallID: "c1", ToolName: "stop", Err: sentinel})
	observeCancelled(runner.Event{Kind: runner.EventCanceled, RunID: "run-2", CallID: "c1", ToolName: "stop", Err: sentinel})
	observeCancelled(runner.Event{Kind: runner.EventRunEnd, RunID: "run-2", Err: sentinel})

	root := cancelled.spansNamed(telemetry.RunSpanName)[0]
	if len(root.errs) != 1 || !errors.Is(root.errs[0], sentinel) {
		t.Fatalf("cancelled run errors = %v, want the sentinel", root.errs)
	}
	stop := cancelled.spansNamed("tool stop")[0]
	if len(stop.errs) != 1 {
		t.Fatalf("cancelling tool errors = %v, want the sentinel", stop.errs)
	}
}

func TestObserverTracksCorrectionRounds(t *testing.T) {
	t.Parallel()

	tracer := &fakeTracer{}
	observe := telemetry.Observer(tracer)

	// First bracketed pass, then the agent's rejection, then the
	// correction round as a second pass under the same RunID.
	observe(runner.Event{Kind: runner.EventRunStart, RunID: "run-1"})
	observe(runner.Event{Kind: runner.EventModelStart, RunID: "run-1", Turn: 0, Attempt: 1})
	observe(runner.Event{Kind: runner.EventModelEnd, RunID: "run-1", Turn: 0, Attempt: 1})
	observe(runner.Event{Kind: runner.EventRunEnd, RunID: "run-1"})
	observe(runner.Event{Kind: runner.EventOutputRejected, RunID: "run-1", Err: errors.New("bad output")})
	observe(runner.Event{Kind: runner.EventRunStart, RunID: "run-1"})
	observe(runner.Event{Kind: runner.EventModelStart, RunID: "run-1", Turn: 0, Attempt: 1})
	observe(runner.Event{Kind: runner.EventModelEnd, RunID: "run-1", Turn: 0, Attempt: 1})
	observe(runner.Event{Kind: runner.EventRunEnd, RunID: "run-1"})

	roots := tracer.spansNamed(telemetry.RunSpanName)
	if len(roots) != 2 {
		t.Fatalf("roots = %d, want 2", len(roots))
	}
	if got := strAttr(t, roots[0], telemetry.AttrRunID); got != "run-1" {
		t.Errorf("both roots carry the run id; first = %q", got)
	}
	if _, ok := attr(roots[0], telemetry.AttrOutputCorrections); ok {
		t.Error("the first pass recorded a correction that had not happened yet")
	}
	if got := intAttr(t, roots[1], telemetry.AttrOutputCorrections); got != 1 {
		t.Errorf("corrections = %d, want 1", got)
	}
	// Consuming the rejection leaves nothing retained.
	observe(runner.Event{Kind: runner.EventRunStart, RunID: "run-1"})
	observe(runner.Event{Kind: runner.EventRunEnd, RunID: "run-1"})
	third := tracer.spansNamed(telemetry.RunSpanName)[2]
	if _, ok := attr(third, telemetry.AttrOutputCorrections); ok {
		t.Error("corrections counted twice")
	}
}

func TestObserverServesInterleavedRuns(t *testing.T) {
	t.Parallel()

	tracer := &fakeTracer{}
	observe := telemetry.Observer(tracer)

	// Two runs alternate events; each keeps its own tree.
	observe(runner.Event{Kind: runner.EventRunStart, RunID: "a"})
	observe(runner.Event{Kind: runner.EventRunStart, RunID: "b"})
	observe(runner.Event{Kind: runner.EventModelStart, RunID: "b", Turn: 0, Attempt: 1})
	observe(runner.Event{Kind: runner.EventModelStart, RunID: "a", Turn: 0, Attempt: 1})
	observe(runner.Event{Kind: runner.EventModelEnd, RunID: "a", Turn: 0, Attempt: 1})
	observe(runner.Event{Kind: runner.EventModelEnd, RunID: "b", Turn: 0, Attempt: 1})
	observe(runner.Event{Kind: runner.EventRunEnd, RunID: "a"})
	observe(runner.Event{Kind: runner.EventRunEnd, RunID: "b"})

	for _, id := range []string{"a", "b"} {
		// Each attempt's parent is its own run's root, not the other's.
		var own *recorded
		for _, root := range tracer.spansNamed(telemetry.RunSpanName) {
			if strAttr(t, root, telemetry.AttrRunID) == id {
				own = root
			}
		}
		if own == nil {
			t.Fatalf("run %q has no root span", id)
		}
		parented := false
		for _, attempt := range tracer.spansNamed(telemetry.ModelSpanName) {
			if attempt.parent == own {
				parented = true
			}
		}
		if !parented {
			t.Fatalf("run %q has no attempt child", id)
		}
	}
	if len(tracer.spans) != 4 {
		t.Fatalf("total spans = %d, want 4", len(tracer.spans))
	}
}

func TestObserverDropsEventsOutsideABracket(t *testing.T) {
	t.Parallel()

	tracer := &fakeTracer{}
	observe := telemetry.Observer(tracer)

	observe(runner.Event{Kind: runner.EventToolStart, RunID: "ghost", CallID: "c1", ToolName: "x"})
	observe(runner.Event{Kind: runner.EventToolEnd, RunID: "ghost", CallID: "c1"})
	observe(runner.Event{Kind: runner.EventModelStart, RunID: "ghost"})
	observe(runner.Event{Kind: runner.EventModelEnd, RunID: "ghost"})
	observe(runner.Event{Kind: runner.EventRunEnd, RunID: "ghost"})

	if len(tracer.spans) != 0 {
		t.Fatalf("spans = %v, want none for an unknown run", tracer.spans)
	}
}

// TestObserverTracesARealRun drives the observer through the actual
// execution loop, so the tree reflects the event order the loop
// really emits.
func TestObserverTracesARealRun(t *testing.T) {
	t.Parallel()

	lookup := tool.MustNew(tool.Tool[struct{}]{
		Name:        "lookup",
		Description: "Looks a value up.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, _ struct{}, _ json.RawMessage) (tool.Result, error) {
			return tool.Text("42"), nil
		},
	})
	m := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "c1", Name: "lookup", Args: json.RawMessage(`{}`)},
		}}, Usage: model.Usage{InputTokens: 3, OutputTokens: 1}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "done"}, Usage: model.Usage{InputTokens: 5, OutputTokens: 2}},
	)

	tracer := &fakeTracer{}
	specs := []model.ToolSpec{{
		Name:        "lookup",
		Description: "Looks a value up.",
		Schema:      json.RawMessage(`{"type":"object"}`),
	}}
	outcome, err := runner.ExecuteWithToolConfig(context.Background(), m, []tool.Tool[struct{}]{lookup}, struct{}{},
		model.Request{Messages: []model.Message{{Role: model.RoleUser, Content: "go"}}, ToolSpecs: specs},
		10, runner.RetryConfig{MaxAttempts: 1}, runner.ToolConfig{}, "",
		telemetry.Observer(tracer), nil, runner.RunIdentity{RunID: "run-9", ConversationID: "con-9"})
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	roots := tracer.spansNamed(telemetry.RunSpanName)
	if len(roots) != 1 {
		t.Fatalf("roots = %d, want 1", len(roots))
	}
	if got := intAttr(t, roots[0], telemetry.AttrUsageInputTokens); got != outcome.Usage.InputTokens {
		t.Errorf("root input tokens = %d, want the outcome's %d", got, outcome.Usage.InputTokens)
	}
	if models := tracer.spansNamed(telemetry.ModelSpanName); len(models) != 2 {
		t.Fatalf("model spans = %d, want 2, one per turn", len(models))
	}
	if tools := tracer.spansNamed("tool lookup"); len(tools) != 1 {
		t.Fatalf("tool spans = %d, want 1", len(tools))
	}
	for _, span := range tracer.spans {
		if !span.ended {
			t.Errorf("span %q did not end", span.name)
		}
	}
}

func TestSpanKindString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		kind telemetry.SpanKind
		want string
	}{
		{telemetry.SpanKindInternal, "internal"},
		{telemetry.SpanKindClient, "client"},
		{telemetry.SpanKind(42), "unknown"},
	}
	for _, test := range tests {
		if got := test.kind.String(); got != test.want {
			t.Errorf("SpanKind(%d).String() = %q, want %q", test.kind, got, test.want)
		}
	}
}

func TestSpanOptions(t *testing.T) {
	t.Parallel()

	tracer := &fakeTracer{}
	tracer.Start(context.Background(), "with-options",
		telemetry.WithSpanKind(telemetry.SpanKindClient),
		telemetry.WithAttributes(telemetry.String("k", "v"), telemetry.Int("n", 1), telemetry.Bool("b", true)))
	span := tracer.spans[0]
	if span.kind != telemetry.SpanKindClient {
		t.Errorf("kind = %s, want client", span.kind)
	}
	if got := strAttr(t, span, "k"); got != "v" {
		t.Errorf("string attribute = %q", got)
	}
	if got := intAttr(t, span, "n"); got != 1 {
		t.Errorf("int attribute = %d", got)
	}
	if value, ok := attr(span, "b"); !ok || value != true {
		t.Errorf("bool attribute = %v, %v", value, ok)
	}

	// The default kind is internal.
	tracer.Start(context.Background(), "default-kind")
	if got := tracer.spans[1].kind; got != telemetry.SpanKindInternal {
		t.Errorf("default kind = %s, want internal", got)
	}
}
