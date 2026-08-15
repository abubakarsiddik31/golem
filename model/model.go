// Package model defines the provider-neutral contract used by Golem agents.
package model

import "context"

// Role identifies the speaker that authored a message.
type Role string

const (
	// RoleSystem provides instructions that guide the model's behavior.
	RoleSystem Role = "system"
	// RoleUser is input supplied by an application user.
	RoleUser Role = "user"
	// RoleAssistant is content produced by a model.
	RoleAssistant Role = "assistant"
)

// Message is a normalized conversational message. Provider adapters are
// responsible for translating it to their native request format.
type Message struct {
	Role    Role
	Content string
}

// Request describes one model generation request.
type Request struct {
	Messages []Message
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
