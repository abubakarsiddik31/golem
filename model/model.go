// Package model defines the provider-neutral contract used by Golem agents.
package model

import (
	"context"
	"encoding/json"
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
// generate stable ones.
type ToolCall struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// Message is a normalized conversational message. Provider adapters are
// responsible for translating it to their native request format. Its JSON
// encoding is a durable, additive-only contract for persisted
// conversations.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content,omitempty"`
	// ToolCalls holds executions requested by an assistant message. When a
	// message carries both content and tool calls, the tool calls decide the
	// turn. Ignored on other roles.
	ToolCalls []ToolCall `json:"toolCalls,omitempty"`
	// ToolCallID and ToolName correlate a RoleTool message with its requested
	// call. Meaningless on other roles.
	ToolCallID string `json:"toolCallId,omitempty"`
	ToolName   string `json:"toolName,omitempty"`
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
}

// Usage reports provider-recorded consumption for a generation. A missing
// value is represented by zeroes when a provider does not expose usage.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// Response is a normalized generation response.
type Response struct {
	Message Message
	Usage   Usage
}

// Model generates a single assistant response for a normalized request.
// Implementations must honor ctx cancellation and return errors rather than
// logging them as a substitute for propagation.
type Model interface {
	Generate(ctx context.Context, request Request) (Response, error)
}
