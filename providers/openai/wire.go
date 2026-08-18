package openai

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/abubakarsiddik31/golem/model"
)

// chatRequest is the chat-completions wire request. Field names follow the
// provider API; omitempty keeps absent tools, calls, and streaming options
// off the wire.
type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Tools    []chatTool    `json:"tools,omitempty"`
	// Stream selects streaming mode; when set, StreamOptions
	// asks the provider to report usage in the final chunk.
	Stream        bool               `json:"stream,omitempty"`
	StreamOptions *chatStreamOptions `json:"stream_options,omitempty"`
	// ResponseFormat requests a response shape; set for structured output.
	ResponseFormat *chatResponseFormat `json:"response_format,omitempty"`
}

type chatStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// chatResponseFormat requests provider-enforced output structure. The
// json_schema form requires a strict-conformant schema from the caller;
// the provider rejects non-conformant schemas with an API error instead
// of the adapter silently relaxing them.
type chatResponseFormat struct {
	Type       string          `json:"type"`
	JSONSchema *chatJSONSchema `json:"json_schema,omitempty"`
}

type chatJSONSchema struct {
	Name   string          `json:"name"`
	Strict bool            `json:"strict"`
	Schema json.RawMessage `json:"schema"`
}

type chatMessage struct {
	Role string `json:"role"`
	// Content is the message text, or a multimodal content-part array
	// when the message carries image parts; see toWireContent.
	Content    any            `json:"content,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

// chatContentPart is one entry of a multimodal content array: the text of
// the message or a provider-fetched image.
type chatContentPart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *chatImageURL `json:"image_url,omitempty"`
}

type chatImageURL struct {
	URL string `json:"url"`
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

// chatChunk is one streamed SSE chunk: a delta for the first
// choice, or usage only in the final chunk where Choices is empty.
type chatChunk struct {
	Choices []chatChunkChoice `json:"choices"`
	Usage   *chatUsage        `json:"usage"`
}

type chatChunkChoice struct {
	Delta chatDeltaMessage `json:"delta"`
}

type chatDeltaMessage struct {
	Role      string              `json:"role,omitempty"`
	Content   string              `json:"content,omitempty"`
	ToolCalls []chatToolCallDelta `json:"tool_calls,omitempty"`
}

type chatToolCallDelta struct {
	// Index correlates fragments of one call across chunks.
	Index    int               `json:"index"`
	ID       string            `json:"id,omitempty"`
	Function chatFunctionDelta `json:"function"`
}

type chatFunctionDelta struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
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
			Content:    toWireContent(message),
			ToolCalls:  toWireToolCalls(message.ToolCalls),
			ToolCallID: message.ToolCallID,
		})
	}
	return wire
}

// toWireContent renders message content: the text alone when no parts are
// attached, or a text part followed by one image part per attached part.
// Inline image data becomes a data URL, the only inline form the
// chat-completions content array accepts.
func toWireContent(message model.Message) any {
	if len(message.Parts) == 0 {
		return message.Content
	}
	parts := make([]chatContentPart, 0, 1+len(message.Parts))
	if message.Content != "" {
		parts = append(parts, chatContentPart{Type: "text", Text: message.Content})
	}
	for _, part := range message.Parts {
		url := part.URL
		if len(part.Data) > 0 {
			url = "data:" + part.MediaType + ";base64," + base64.StdEncoding.EncodeToString(part.Data)
		}
		parts = append(parts, chatContentPart{Type: "image_url", ImageURL: &chatImageURL{URL: url}})
	}
	return parts
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

// normalizeArguments converts stringified wire arguments to raw JSON,
// mapping an empty string to an empty object.
func normalizeArguments(arguments string) json.RawMessage {
	args := strings.TrimSpace(arguments)
	if args == "" {
		args = "{}"
	}
	return json.RawMessage(args)
}

// contentText extracts the text of a decoded content field. Assistant
// responses the agent consumes are text; any other shape decodes as empty.
func contentText(content any) string {
	text, _ := content.(string)
	return text
}

// fromWireResponse normalizes a chat-completions body. The first choice
// wins; stringified arguments become raw JSON, with an empty
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
		calls = append(calls, model.ToolCall{
			ID:   call.ID,
			Name: call.Function.Name,
			Args: normalizeArguments(call.Function.Arguments),
		})
	}

	return model.Response{
		Message: model.Message{
			Role:      model.RoleAssistant,
			Content:   contentText(choice.Content),
			ToolCalls: calls,
		},
		Usage: model.Usage{
			InputTokens:  wire.Usage.PromptTokens,
			OutputTokens: wire.Usage.CompletionTokens,
		},
	}, nil
}
