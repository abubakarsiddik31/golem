package model

import "context"

// Delta is one streamed fragment of an assistant response. A
// delta carries whichever fragments the provider emitted: a piece of text
// content, a thinking fragment, one or more tool-call fragments, or any
// combination. Deltas are in-flight progress, not a persistence contract:
// they carry no JSON tags and no stability promise, unlike Message.
type Delta struct {
	// Content is a text fragment to append to the response content.
	Content string
	// Thinking holds reasoning fragments; see ThinkingDelta. Providers that
	// reason emit these before the text content of the same response.
	Thinking []ThinkingDelta
	// ToolCalls holds tool-call fragments; Index correlates each fragment
	// with its call across deltas.
	ToolCalls []ToolCallDelta
}

// ThinkingDelta is a fragment of one streamed thinking block. Index
// correlates the fragment with its block across deltas, matching the
// block's position in the assembled message's Thinking. Text is a piece of
// the visible reasoning; Signature arrives complete when the provider
// emits it.
type ThinkingDelta struct {
	Index     int
	Text      string
	Signature string
}

// ToolCallDelta is a fragment of one streamed tool call. ID and Name are
// non-empty only on the first fragment of their call; ArgsFragment is a
// piece of the JSON arguments text, empty when the fragment carries only
// identification. Signature, when non-empty, is the provider's reasoning
// evidence bound to the call and arrives complete.
type ToolCallDelta struct {
	Index int
	ID    string
	Name  string
	// ArgsFragment is a fragment of the call's JSON arguments.
	ArgsFragment string
	// Signature is provider-produced reasoning evidence for the call,
	// replayed through history for verification; see ToolCall.
	Signature string
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
