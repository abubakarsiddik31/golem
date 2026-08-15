package openai

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/abubakarsiddik31/golem/model"
)

// chatRequest is the chat-completions wire request. Field names follow the
// provider API; omitempty keeps absent tools and calls off the wire.
type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Tools    []chatTool    `json:"tools,omitempty"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type chatToolCall struct {
	// ID identifies the call; the provider requires it on tool replies.
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function chatFunctionCall `json:"function"`
}

type chatFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatTool struct {
	Type     string       `json:"type"`
	Function chatFunction `json:"function"`
}

type chatFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type chatResponse struct {
	Choices []chatChoice `json:"choices"`
	Usage   chatUsage    `json:"usage"`
}

type chatChoice struct {
	Message chatMessage `json:"message"`
}

type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type chatErrorEnvelope struct {
	Error chatErrorBody `json:"error"`
}

type chatErrorBody struct {
	Code    any    `json:"code"`
	Message string `json:"message"`
}

func toWireMessages(messages []model.Message) []chatMessage {
	wire := make([]chatMessage, 0, len(messages))
	for _, message := range messages {
		wire = append(wire, chatMessage{
			Role:       toWireRole(message.Role),
			Content:    message.Content,
			ToolCalls:  toWireToolCalls(message.ToolCalls),
			ToolCallID: message.ToolCallID,
		})
	}
	return wire
}

func toWireRole(role model.Role) string {
	switch role {
	case model.RoleSystem:
		return "system"
	case model.RoleAssistant:
		return "assistant"
	case model.RoleTool:
		return "tool"
	default:
		return "user"
	}
}

func toWireToolCalls(calls []model.ToolCall) []chatToolCall {
	if len(calls) == 0 {
		return nil
	}
	wire := make([]chatToolCall, 0, len(calls))
	for _, call := range calls {
		wire = append(wire, chatToolCall{
			ID:   call.ID,
			Type: "function",
			Function: chatFunctionCall{
				Name:      call.Name,
				Arguments: string(call.Args),
			},
		})
	}
	return wire
}

func toWireTools(specs []model.ToolSpec) []chatTool {
	if len(specs) == 0 {
		return nil
	}
	wire := make([]chatTool, 0, len(specs))
	for _, spec := range specs {
		wire = append(wire, chatTool{
			Type: "function",
			Function: chatFunction{
				Name:        spec.Name,
				Description: spec.Description,
				Parameters:  spec.Schema,
			},
		})
	}
	return wire
}

// fromWireResponse normalizes a chat-completions body. The first choice
// wins per ADR 0003; stringified arguments become raw JSON, with an empty
// string mapped to an empty object.
func fromWireResponse(payload []byte) (model.Response, error) {
	var wire chatResponse
	if err := json.Unmarshal(payload, &wire); err != nil {
		return model.Response{}, &DecodeError{Stage: "decode response body", Err: err}
	}
	if len(wire.Choices) == 0 {
		return model.Response{}, &DecodeError{
			Stage: "decode response body",
			Err:   fmt.Errorf("response contained no choices"),
		}
	}

	choice := wire.Choices[0].Message
	calls := make([]model.ToolCall, 0, len(choice.ToolCalls))
	for _, call := range choice.ToolCalls {
		args := strings.TrimSpace(call.Function.Arguments)
		if args == "" {
			args = "{}"
		}
		calls = append(calls, model.ToolCall{
			ID:   call.ID,
			Name: call.Function.Name,
			Args: json.RawMessage(args),
		})
	}

	return model.Response{
		Message: model.Message{
			Role:      model.RoleAssistant,
			Content:   choice.Content,
			ToolCalls: calls,
		},
		Usage: model.Usage{
			InputTokens:  wire.Usage.PromptTokens,
			OutputTokens: wire.Usage.CompletionTokens,
		},
	}, nil
}
