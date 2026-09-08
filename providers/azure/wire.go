package azure

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/abubakarsiddik31/golem/model"
)

// chatRequest is the chat-completions wire request. Field names follow the
// provider API; omitempty keeps absent tools, calls, and streaming options
// off the wire. The model is not on the wire: Azure addresses it by
// deployment in the URL.
type chatRequest struct {
	Messages []chatMessage `json:"messages"`
	Tools    []chatTool    `json:"tools,omitempty"`
	// Sampling and length controls; unset fields stay off the wire.
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	MaxTokens   int      `json:"max_tokens,omitempty"`
	// ReasoningEffort asks reasoning models how much to think before
	// answering; empty stays off the wire.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
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
	// ReasoningContent is the non-standard reasoning field some
	// OpenAI-compatible endpoints return on assistant messages. It is
	// captured into the message's thinking and never sent back.
	ReasoningContent string `json:"reasoning_content,omitempty"`
}

// chatContentPart is one entry of a multimodal content array: the text of
// the message or a provider-fetched image.
// chatContentPart is one entry of a multimodal content array: the text
// of the message, an image, an inline document, or inline audio.
type chatContentPart struct {
	Type       string          `json:"type"`
	Text       string          `json:"text,omitempty"`
	ImageURL   *chatImageURL   `json:"image_url,omitempty"`
	InputAudio *chatInputAudio `json:"input_audio,omitempty"`
	File       *chatFileInput  `json:"file,omitempty"`
}

type chatImageURL struct {
	URL string `json:"url"`
}

// chatInputAudio is inline audio; the endpoint accepts wav and mp3 only.
type chatInputAudio struct {
	Data   string `json:"data"`
	Format string `json:"format"`
}

// chatFileInput is one inline document; file_data is the base64 payload
// and filename labels it for the model.
type chatFileInput struct {
	FileData string `json:"file_data"`
	Filename string `json:"filename,omitempty"`
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
	// FinishReason is the provider's terminal cause: stop, length,
	// tool_calls, function_call (legacy), or content_filter.
	FinishReason string `json:"finish_reason"`
}

// finishReason translates the chat-completions finish_reason vocabulary
// onto the shared model constants: stop and its legacy function-call
// sibling are a tool request, everything undocumented is FinishOther.
func finishReason(reason string) model.FinishReason {
	switch reason {
	case "stop":
		return model.FinishStop
	case "length":
		return model.FinishLength
	case "tool_calls", "function_call":
		return model.FinishToolCall
	case "content_filter":
		return model.FinishContentFilter
	case "":
		return ""
	default:
		return model.FinishOther
	}
}

type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	// Cached tokens are a subset of PromptTokens; reasoning tokens are a
	// subset of CompletionTokens.
	PromptTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

// detailUsage lifts the optional detail objects into the normalized
// usage: cached input from prompt_tokens_details, reasoning output from
// completion_tokens_details.
func (u chatUsage) detailUsage() (cacheRead, reasoning int) {
	if u.PromptTokensDetails != nil {
		cacheRead = u.PromptTokensDetails.CachedTokens
	}
	if u.CompletionTokensDetails != nil {
		reasoning = u.CompletionTokensDetails.ReasoningTokens
	}
	return cacheRead, reasoning
}

// chatChunk is one streamed SSE chunk: a delta for the first
// choice, or usage only in the final chunk where Choices is empty.
type chatChunk struct {
	Choices []chatChunkChoice `json:"choices"`
	Usage   *chatUsage        `json:"usage"`
}

type chatChunkChoice struct {
	Delta chatDeltaMessage `json:"delta"`
	// FinishReason terminates the stream on the final choice chunk.
	FinishReason string `json:"finish_reason"`
}

type chatDeltaMessage struct {
	Role      string              `json:"role,omitempty"`
	Content   string              `json:"content,omitempty"`
	ToolCalls []chatToolCallDelta `json:"tool_calls,omitempty"`
	// ReasoningContent carries a fragment of the model's reasoning on
	// deployments that stream it.
	ReasoningContent string `json:"reasoning_content,omitempty"`
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

func toWireMessages(messages []model.Message) ([]chatMessage, error) {
	wire := make([]chatMessage, 0, len(messages))
	for _, message := range messages {
		content, err := toWireContent(message)
		if err != nil {
			return nil, err
		}
		wire = append(wire, chatMessage{
			Role:       toWireRole(message.Role),
			Content:    content,
			ToolCalls:  toWireToolCalls(message.ToolCalls),
			ToolCallID: message.ToolCallID,
		})
	}
	return wire, nil
}

// toWireContent renders message content: the text alone when no parts are
// attached, or a text part followed by one content part per attached
// part. Inline image data becomes a data URL; documents send base64
// `file` parts and audio sends `input_audio` parts. Unsupported
// combinations — document or audio URLs, non-PDF documents, audio that
// is not wav or mp3, and video, which the chat-completions content
// array cannot carry — fail before any request.
func toWireContent(message model.Message) (any, error) {
	if len(message.Parts) == 0 {
		return message.Content, nil
	}
	parts := make([]chatContentPart, 0, 1+len(message.Parts))
	if message.Content != "" {
		parts = append(parts, chatContentPart{Type: "text", Text: message.Content})
	}
	for i, part := range message.Parts {
		switch part.Kind {
		case model.PartImage:
			url := part.URL
			if len(part.Data) > 0 {
				url = "data:" + part.MediaType + ";base64," + base64.StdEncoding.EncodeToString(part.Data)
			}
			parts = append(parts, chatContentPart{Type: "image_url", ImageURL: &chatImageURL{URL: url}})
		case model.PartDocument:
			if part.URL != "" {
				return nil, &DecodeError{Stage: "encode request", Err: fmt.Errorf(
					"document part %d carries a URL; chat completions accept inline file data only — fetch the document and attach it with golem.WithPromptParts(model.DocumentData(...))", i)}
			}
			if part.MediaType != "application/pdf" {
				return nil, &DecodeError{Stage: "encode request", Err: fmt.Errorf(
					"document part %d media type %q; chat completions accept inline application/pdf documents", i, part.MediaType)}
			}
			parts = append(parts, chatContentPart{Type: "file", File: &chatFileInput{
				FileData: base64.StdEncoding.EncodeToString(part.Data),
				Filename: "document.pdf",
			}})
		case model.PartAudio:
			if part.URL != "" {
				return nil, &DecodeError{Stage: "encode request", Err: fmt.Errorf(
					"audio part %d carries a URL; chat completions accept inline audio only — attach it with golem.WithPromptParts(model.AudioData(...))", i)}
			}
			format, ok := audioFormat(part.MediaType)
			if !ok {
				return nil, &DecodeError{Stage: "encode request", Err: fmt.Errorf(
					"audio part %d media type %q; chat completions accept audio/wav or audio/mpeg", i, part.MediaType)}
			}
			parts = append(parts, chatContentPart{Type: "input_audio", InputAudio: &chatInputAudio{
				Data:   base64.StdEncoding.EncodeToString(part.Data),
				Format: format,
			}})
		default:
			return nil, &DecodeError{Stage: "encode request", Err: fmt.Errorf(
				"part %d kind %q; chat completions accept image, document, and audio parts — use the gemini adapter for video", i, part.Kind)}
		}
	}
	return parts, nil
}

// audioFormat maps a media type onto the input_audio format vocabulary.
func audioFormat(mediaType string) (string, bool) {
	switch mediaType {
	case "audio/wav", "audio/x-wav", "audio/wave":
		return "wav", true
	case "audio/mpeg", "audio/mp3":
		return "mp3", true
	}
	return "", false
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

// contentText extracts the text of a decoded content field. Assistant
// responses the agent consumes are text; any other shape decodes as empty.
func contentText(content any) string {
	text, _ := content.(string)
	return text
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
	var thinking []model.ThinkingBlock
	if choice.ReasoningContent != "" {
		thinking = []model.ThinkingBlock{model.ThinkingText(choice.ReasoningContent)}
	}

	return model.Response{
		Message: model.Message{
			Role:      model.RoleAssistant,
			Content:   contentText(choice.Content),
			ToolCalls: calls,
			Thinking:  thinking,
		},
		Usage:        usageWithDetails(wire.Usage),
		FinishReason: finishReason(wire.Choices[0].FinishReason),
	}, nil
}

// usageWithDetails translates the totals plus their optional detail
// objects into the normalized usage.
func usageWithDetails(u chatUsage) model.Usage {
	cacheRead, reasoning := u.detailUsage()
	return model.Usage{
		InputTokens:     u.PromptTokens,
		OutputTokens:    u.CompletionTokens,
		CacheReadTokens: cacheRead,
		ReasoningTokens: reasoning,
	}
}
