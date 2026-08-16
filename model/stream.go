package model

import "context"

// Delta is one streamed fragment of an assistant response. A
// delta carries whichever fragments the provider emitted: a piece of text
// content, one or more tool-call fragments, or both. Deltas are in-flight
// progress, not a persistence contract: they carry no JSON tags and no
// stability promise, unlike Message.
type Delta struct {
	// Content is a text fragment to append to the response content.
	Content string
	// ToolCalls holds tool-call fragments; Index correlates each fragment
	// with its call across deltas.
	ToolCalls []ToolCallDelta
}

// ToolCallDelta is a fragment of one streamed tool call. ID and Name are
// non-empty only on the first fragment of their call; ArgsFragment is a
// piece of the JSON arguments text, empty when the fragment carries only
// identification.
type ToolCallDelta struct {
	Index int
	ID    string
	Name  string
	// ArgsFragment is a fragment of the call's JSON arguments.
	ArgsFragment string
}

// StreamingModel is an optional streaming capability of Model:
// implementations that can stream advertise it, and the rest are
// unaffected. GenerateStream behaves exactly like Generate — same request
// translation, same error classification, same normalization of the
// returned Response — except it reports progress along the way.
//
// onDelta is called synchronously, once per fragment, while the response
// is being read. A non-nil return stops the stream and is returned by
// GenerateStream as-is: stopping is the caller's decision, not a provider
// failure. Cancellation otherwise flows through ctx like any Generate
// call.
type StreamingModel interface {
	Model
	GenerateStream(ctx context.Context, request Request, onDelta func(Delta) error) (Response, error)
}
