// Package model defines the provider-neutral contract used by Golem agents.
package model

import (
	"context"
	"encoding/json"
	"fmt"
)

// Role identifies the speaker that authored a message.
type Role string

const (
	// RoleSystem provides instructions that guide the model's behavior.
	RoleSystem Role = "system"
	// RoleUser is input supplied by an application user.
	RoleUser Role = "user"
	// RoleAssistant is content produced by a model.
	RoleAssistant Role = "assistant"
	// RoleTool carries the result of one tool execution back to the model.
	// ToolCallID and ToolName correlate the message with its requested call.
	RoleTool Role = "tool"
)

// ToolCall is a model-requested tool execution on an assistant message. Args
// is untrusted model output: it stays raw JSON until a tool decodes and
// validates it explicitly. Providers that omit call IDs require adapters to
// generate stable ones. Signature carries the provider's opaque reasoning
// evidence bound to the call — Gemini's thoughtSignature — which adapters
// replay unchanged so the next turn verifies; it is empty for providers
// that bind reasoning to the message instead.
type ToolCall struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
	// Signature is provider-produced reasoning evidence returned with the
	// call. Treat it as immutable: it verifies the reasoning that led to
	// the call and must round-trip through history unmodified.
	Signature string `json:"signature,omitempty"`
}

// Message is a normalized conversational message. Provider adapters are
// responsible for translating it to their native request format. Its JSON
// encoding is a durable, additive-only contract for persisted
// conversations.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content,omitempty"`
	// Parts carries non-text content appended after Content, such as
	// images on a user message; see Part. Additive to the durable JSON
	// contract: text stays in Content and older history decodes with no
	// parts.
	Parts []Part `json:"parts,omitempty"`
	// ToolCalls holds executions requested by an assistant message. When a
	// message carries both content and tool calls, the tool calls decide the
	// turn. Ignored on other roles.
	ToolCalls []ToolCall `json:"toolCalls,omitempty"`
	// ToolCallID and ToolName correlate a RoleTool message with its requested
	// call. Meaningless on other roles.
	ToolCallID string `json:"toolCallId,omitempty"`
	ToolName   string `json:"toolName,omitempty"`
	// Thinking carries the model's reasoning blocks on an assistant
	// message, in provider order; see ThinkingBlock. Adapters replay the
	// blocks — signatures included — so the next turn verifies the
	// reasoning. Ignored on other roles, and rejected by the agent's
	// history validation there.
	Thinking []ThinkingBlock `json:"thinking,omitempty"`
}

// ThinkingBlock is one block of model reasoning on an assistant message:
// visible reasoning text with its verifiable Signature, or the provider's
// encrypted Redacted payload. Exactly one of Text or Redacted is set, and
// Signature requires Text; constructors make the common paths
// well-formed and Validate is the boundary check for blocks that arrive
// through decoded history. Signature and Redacted are provider-produced
// opaque values: treat them as immutable and round-trip them unchanged.
type ThinkingBlock struct {
	// Text is the visible reasoning content.
	Text string `json:"text,omitempty"`
	// Signature is the provider's verification token for Text. Adapters
	// replay it so later turns can verify the reasoning.
	Signature string `json:"signature,omitempty"`
	// Redacted is the provider's encrypted reasoning payload for reasoning
	// the provider declined to show. Adapters replay it unchanged.
	Redacted string `json:"redacted,omitempty"`
}

// ThinkingText returns a block carrying visible reasoning text.
func ThinkingText(text string) ThinkingBlock {
	return ThinkingBlock{Text: text}
}

// ThinkingSigned returns a block carrying visible reasoning text with the
// provider's verification signature, as adapters produce it.
func ThinkingSigned(text, signature string) ThinkingBlock {
	return ThinkingBlock{Text: text, Signature: signature}
}

// ThinkingRedacted returns a block carrying the provider's encrypted
// reasoning payload.
func ThinkingRedacted(data string) ThinkingBlock {
	return ThinkingBlock{Redacted: data}
}

// Validate reports whether the block is well-formed. The agent validates
// thinking at run start, before any model call; adapters may assume the
// blocks that reach them passed this check.
func (b ThinkingBlock) Validate() error {
	switch {
	case b.Text == "" && b.Redacted == "":
		return fmt.Errorf("model: thinking block carries neither text nor redacted data")
	case b.Text != "" && b.Redacted != "":
		return fmt.Errorf("model: thinking block carries both text and redacted data; set exactly one")
	case b.Signature != "" && b.Redacted != "":
		return fmt.Errorf("model: redacted thinking cannot carry a signature")
	}
	return nil
}

// PartKind identifies the kind of non-text content a Part carries.
type PartKind string

// PartImage is an image attached to a user message.
const PartImage PartKind = "image"

// Part is one non-text piece of message content, carried on user messages
// alongside the text in Content. Exactly one of URL or Data is set, and
// Data requires MediaType; constructors make the common path well-formed
// and Validate is the boundary check for parts that arrive through decoded
// history. Adapters translate parts to their native multimodal form.
type Part struct {
	Kind      PartKind `json:"kind"`
	URL       string   `json:"url,omitempty"`
	Data      []byte   `json:"data,omitempty"`
	MediaType string   `json:"mediaType,omitempty"`
}

// ImageURL attaches an image reachable at url; the provider fetches it.
func ImageURL(url string) Part {
	return Part{Kind: PartImage, URL: url}
}

// ImageData attaches inline image bytes with their media type, such as
// "image/png". Data is application-owned: treat it as immutable once
// attached, because a Part does not copy it.
func ImageData(mediaType string, data []byte) Part {
	return Part{Kind: PartImage, MediaType: mediaType, Data: data}
}

// Validate reports whether the part is well-formed. The agent validates
// parts at run start, before any model call; adapters may assume the parts
// that reach them passed this check.
func (p Part) Validate() error {
	switch {
	case p.Kind != PartImage:
		return fmt.Errorf("model: unsupported part kind %q", p.Kind)
	case p.URL != "" && len(p.Data) > 0:
		return fmt.Errorf("model: image part carries both a URL and inline data; set exactly one")
	case p.URL == "" && len(p.Data) == 0:
		return fmt.Errorf("model: image part has neither a URL nor inline data")
	case len(p.Data) > 0 && p.MediaType == "":
		return fmt.Errorf("model: image part with inline data requires a media type")
	}
	return nil
}

// ToolSpec advertises one tool to the model. Schema is a JSON Schema
// document describing the arguments object; it is inspectable without
// executing the tool.
type ToolSpec struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

// Request describes one model generation request.
type Request struct {
	Messages []Message
	// ToolSpecs lists the tools the model may request this turn.
	ToolSpecs []ToolSpec
	// OutputSchema is an optional JSON Schema document describing the
	// expected final answer. Adapters that support structured output map
	// it to their native mechanism; adapters that do not ignore it. The
	// decoder remains the validation boundary: the schema describes, it
	// does not validate.
	OutputSchema json.RawMessage
}

// Usage reports provider-recorded consumption for a generation. A missing
// value is represented by zeroes when a provider does not expose usage.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// FinishReason is the provider's normalized terminal cause for one model
// response: why the model stopped generating. Adapters translate their
// provider's wire vocabulary onto the shared constants — each adapter's
// documentation pins the translation — and an undocumented value maps to
// FinishOther. The zero value means the provider reported nothing.
type FinishReason string

const (
	// FinishStop is a natural completion, or a stop sequence: the model
	// produced a full answer.
	FinishStop FinishReason = "stop"
	// FinishLength is a truncation: the response hit the output token or
	// length cap before finishing. Content may be cut off mid-sentence —
	// or mid-JSON, which the decoder then rejects.
	FinishLength FinishReason = "length"
	// FinishToolCall is a turn that ended because the model requested
	// tool calls.
	FinishToolCall FinishReason = "tool_call"
	// FinishContentFilter is a stop forced by the provider's safety
	// systems: content filters, guardrails, refusal, or blocked-content
	// categories. The response may still carry refusal or partial text.
	FinishContentFilter FinishReason = "content_filter"
	// FinishOther is a terminal cause outside the shared vocabulary,
	// preserved so an unrecognized provider value is still visible as
	// "not stop".
	FinishOther FinishReason = "other"
)

// Response is a normalized generation response.
type Response struct {
	Message Message
	Usage   Usage
	// FinishReason is the provider's terminal cause for this response;
	// empty when the provider reported none. Fakes that do not set it
	// leave it empty by design.
	FinishReason FinishReason
}

// Model generates a single assistant response for a normalized request.
// Implementations must honor ctx cancellation and return errors rather than
// logging them as a substitute for propagation.
type Model interface {
	Generate(ctx context.Context, request Request) (Response, error)
}
