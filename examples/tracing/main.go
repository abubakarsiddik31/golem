// Command tracing turns a run's event stream into spans through the
// telemetry port, the way an OpenTelemetry adapter would — the tracer
// here is an in-memory recorder that prints the finished tree, so the
// example runs offline with no SDK and no credentials. The guide shows
// the same adapter written against go.opentelemetry.io/otel.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/telemetry"
	"github.com/abubakarsiddik31/golem/testmodel"
	"github.com/abubakarsiddik31/golem/tool"
)

func main() {
	getPlayerName := tool.MustNew(tool.Tool[string]{
		Name:        "get_player_name",
		Description: "Get the player's name.",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Exec: func(ctx context.Context, playerName string, args json.RawMessage) (tool.Result, error) {
			return tool.Text(playerName), nil
		},
	})

	client := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: "get_player_name", Args: json.RawMessage(`{}`)},
		}}, Usage: model.Usage{InputTokens: 210, OutputTokens: 8}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "Anne wins"},
			Usage: model.Usage{InputTokens: 340, OutputTokens: 12}},
	)

	// The application owns the exporter: its tracer implements the
	// port, and the port's observer drives it from the event stream.
	tracer := &printingTracer{}
	agent, err := golem.New[string, string](client,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithTools[string, string](getPlayerName),
		golem.WithRunEvents[string, string](telemetry.Observer(tracer)),
	)
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}

	result, err := agent.Run(context.Background(), golem.RunContext[string]{Deps: "Anne"}, "who wins?")
	if err != nil {
		fmt.Println("Run:", err)
		return
	}
	fmt.Println("output:", result.Output)
	fmt.Println()
	tracer.print()
}

// printingTracer implements telemetry.Tracer by recording spans in
// memory. An OpenTelemetry adapter has the same shape: apply the
// options, start an SDK span, and return a span whose SetAttributes,
// RecordError, and End forward to the SDK's.
type printingTracer struct {
	mu    sync.Mutex
	spans []*printedSpan
}

type printedSpan struct {
	name      string
	parent    *printedSpan
	attrs     []telemetry.Attribute
	errs      []error
	startedAt time.Time
	endedAt   time.Time
}

func (t *printingTracer) Start(ctx context.Context, name string, opts ...telemetry.SpanOption) (context.Context, telemetry.Span) {
	cfg := telemetry.SpanConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	span := &printedSpan{name: name, attrs: cfg.Attributes, startedAt: time.Now()}
	if parent, ok := ctx.Value(parentKey{}).(*printedSpan); ok {
		span.parent = parent
	}
	t.spans = append(t.spans, span)
	return context.WithValue(ctx, parentKey{}, span), span
}

type parentKey struct{}

func (s *printedSpan) SetAttributes(attrs ...telemetry.Attribute) {
	s.attrs = append(s.attrs, attrs...)
}

func (s *printedSpan) RecordError(err error) {
	s.errs = append(s.errs, err)
}

func (s *printedSpan) End() {
	s.endedAt = time.Now()
}

// print renders the finished forest: one line per span, children
// indented under their parent, attributes inlined.
func (t *printingTracer) print() {
	t.mu.Lock()
	defer t.mu.Unlock()

	var walk func(span *printedSpan, depth int)
	walk = func(span *printedSpan, depth int) {
		fmt.Printf("%s%s  (%s)\n", strings.Repeat("  ", depth), span.name, span.duration())
		for _, attr := range span.attrs {
			fmt.Printf("%s  %s = %v\n", strings.Repeat("  ", depth), attr.Key, attr.Value)
		}
		for _, err := range span.errs {
			fmt.Printf("%s  error = %v\n", strings.Repeat("  ", depth), err)
		}
		for _, child := range t.spans {
			if child.parent == span {
				walk(child, depth+1)
			}
		}
	}
	for _, span := range t.spans {
		if span.parent == nil {
			walk(span, 0)
		}
	}
}

func (s *printedSpan) duration() string {
	return s.endedAt.Sub(s.startedAt).Round(time.Microsecond).String()
}
