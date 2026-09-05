package bedrock

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/abubakarsiddik31/golem/model"
)

// converseRequest is the Converse API wire request. The model stays off
// the wire: it is addressed by ID in the URL.
type converseRequest struct {
	Messages        []wireMessage     `json:"messages"`
	System          []wireSystem      `json:"system,omitempty"`
	ToolConfig      *wireToolConfig   `json:"toolConfig,omitempty"`
	InferenceConfig *wireInference    `json:"inferenceConfig,omitempty"`
	OutputConfig    *wireOutputConfig `json:"outputConfig,omitempty"`
	// AdditionalModelRequestFields passes model-specific request fields
	// through Converse; the thinking configuration rides here.
	AdditionalModelRequestFields json.RawMessage `json:"additionalModelRequestFields,omitempty"`
}

// wireMessage is one conversational turn. Roles alternate between "user"
// and "assistant"; tool results travel as user-turn blocks that must
// follow the assistant turn requesting them.
type wireMessage struct {
	Role    string      `json:"role"`
	Content []wireBlock `json:"content"`
}

// wireBlock is one content block of a turn: text, the model's signed
// reasoning, a model-requested tool use on assistant turns, or the
// outcome of one on user turns.
type wireBlock struct {
	Text string `json:"text,omitempty"`
	// ReasoningContent carries the model's reasoning on assistant turns:
	// visible text with its verification signature, or the provider's
	// encrypted payload.
	ReasoningContent *wireReasoningContent `json:"reasoningContent,omitempty"`
	// ToolUse carries a model-requested execution. ToolUseID is the
	// provider's own call ID, passed through unmodified.
	ToolUse *wireToolUse `json:"toolUse,omitempty"`
	// ToolResult carries the execution outcome back.
	ToolResult *wireToolResult `json:"toolResult,omitempty"`
	// Image carries an inline image block; see wireImage for the formats
	// Converse accepts.
	Image *wireImage `json:"image,omitempty"`
}

// wireImage is an inline image block. Converse accepts base64 bytes only,
// in the png, jpeg, gif, and webp formats.
type wireImage struct {
	Format string       `json:"format"`
	Source wireImageSrc `json:"source"`
}

// wireImageSrc carries the image payload. Bytes is base64-encoded; the
// Converse API decodes it.
type wireImageSrc struct {
	Bytes string `json:"bytes"`
}

type wireToolUse struct {
	ToolUseID string          `json:"toolUseId"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
}

// wireReasoningContent is one assistant reasoning block: visible reasoning
// text with its verification signature, or the provider's encrypted
// payload for reasoning it declined to show.
type wireReasoningContent struct {
	ReasoningText   *wireReasoningText `json:"reasoningText,omitempty"`
	RedactedContent string             `json:"redactedContent,omitempty"`
}

type wireReasoningText struct {
	Text      string `json:"text,omitempty"`
	Signature string `json:"signature,omitempty"`
}

type wireToolResult struct {
	ToolUseID string         `json:"toolUseId"`
	Content   []wireTextOnly `json:"content"`
}

type wireTextOnly struct {
	Text string `json:"text"`
}

type wireSystem struct {
	Text string `json:"text"`
}

type wireToolConfig struct {
	Tools []wireToolDeclaration `json:"tools"`
}

type wireToolDeclaration struct {
	ToolSpec struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		InputSchema wireInputSchema `json:"inputSchema"`
	} `json:"toolSpec"`
}

type wireInputSchema struct {
	JSON json.RawMessage `json:"json"`
}

type wireInference struct {
	MaxTokens int `json:"maxTokens,omitempty"`
	// Sampling controls; unset fields stay off the wire.
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"topP,omitempty"`
}

// wireOutputConfig requests provider-enforced output structure; set for
// structured output. The provider receives the schema as a string. Effort
// scales how much the model reasons and works; it rides the same object
// as a sibling of the format.
type wireOutputConfig struct {
	TextFormat *wireTextFormat `json:"textFormat,omitempty"`
	Effort     string          `json:"effort,omitempty"`
}

type wireTextFormat struct {
	Type      string               `json:"type"`
	Structure *wireFormatStructure `json:"structure,omitempty"`
}

type wireFormatStructure struct {
	JSONSchema *wireJSONSchemaDef `json:"jsonSchema,omitempty"`
}

type wireJSONSchemaDef struct {
	Name   string `json:"name,omitempty"`
	Schema string `json:"schema"`
}

// converseResponse is the Converse API wire response.
type converseResponse struct {
	Output struct {
		Message wireMessage `json:"message"`
	} `json:"output"`
	StopReason string `json:"stopReason"`
	Usage      struct {
		InputTokens  int `json:"inputTokens"`
		OutputTokens int `json:"outputTokens"`
	} `json:"usage"`
}

// toWireMessages converts normalized messages into Converse turns.
// System messages become top-level system blocks; consecutive messages
// of the same role — tool results after an assistant turn, or a user
// prompt after tool results — merge into one turn, because tool results
// must ride the user turn that follows the request.
// toWireMessages converts normalized messages into Converse turns. It
// fails before anything is sent when a message carries content Converse
// cannot express, such as an image referenced by URL.
func toWireMessages(messages []model.Message) (system []wireSystem, turns []wireMessage, err error) {
	for _, message := range messages {
		var role string
		var blocks []wireBlock
		switch message.Role {
		case model.RoleSystem:
			system = append(system, wireSystem{Text: message.Content})
			continue
		case model.RoleAssistant:
			role = "assistant"
			blocks = assistantBlocks(message)
		case model.RoleTool:
			role = "user"
			blocks = []wireBlock{{ToolResult: &wireToolResult{
				ToolUseID: message.ToolCallID,
				Content:   []wireTextOnly{{Text: message.Content}},
			}}}
		default:
			role = "user"
			blocks, err = userBlocks(message)
			if err != nil {
				return nil, nil, err
			}
		}
		if len(turns) > 0 && turns[len(turns)-1].Role == role {
			turns[len(turns)-1].Content = append(turns[len(turns)-1].Content, blocks...)
			continue
		}
		turns = append(turns, wireMessage{Role: role, Content: blocks})
	}
	return system, turns, nil
}

// assistantBlocks renders an assistant message: its reasoning blocks
// first — the provider verifies them against the rest of the turn —,
// then the text as a text block when present, then one toolUse block per
// requested call.
func assistantBlocks(message model.Message) []wireBlock {
	var blocks []wireBlock
	for _, block := range message.Thinking {
		if block.Redacted != "" {
			blocks = append(blocks, wireBlock{ReasoningContent: &wireReasoningContent{RedactedContent: block.Redacted}})
			continue
		}
		blocks = append(blocks, wireBlock{ReasoningContent: &wireReasoningContent{
			ReasoningText: &wireReasoningText{Text: block.Text, Signature: block.Signature},
		}})
	}
	if message.Content != "" {
		blocks = append(blocks, wireBlock{Text: message.Content})
	}
	for _, call := range message.ToolCalls {
		blocks = append(blocks, wireBlock{ToolUse: &wireToolUse{
			ToolUseID: call.ID,
			Name:      call.Name,
			Input:     normalizeInput(call.Args),
		}})
	}
	return blocks
}

// userBlocks renders a user message: its text as a text block when
// present, followed by one image block per attached part. Converse
// accepts inline bytes in the png, jpeg, gif, and webp formats; any other
// form — a URL, or an unknown media type — fails with
// ErrUnsupportedContent instead of being silently dropped.
func userBlocks(message model.Message) ([]wireBlock, error) {
	var blocks []wireBlock
	if message.Content != "" {
		blocks = append(blocks, wireBlock{Text: message.Content})
	}
	for _, part := range message.Parts {
		if part.URL != "" {
			return nil, fmt.Errorf("%w: image URL parts; fetch the image and attach it inline with golem.WithPromptImageData", ErrUnsupportedContent)
		}
		format, ok := strings.CutPrefix(part.MediaType, "image/")
		if !ok || format == "" {
			return nil, fmt.Errorf("%w: image media type %q; Converse accepts image/png, image/jpeg, image/gif, or image/webp", ErrUnsupportedContent, part.MediaType)
		}
		blocks = append(blocks, wireBlock{Image: &wireImage{
			Format: format,
			Source: wireImageSrc{Bytes: base64.StdEncoding.EncodeToString(part.Data)},
		}})
	}
	if len(blocks) == 0 {
		blocks = []wireBlock{{Text: message.Content}}
	}
	return blocks, nil
}

func toWireToolConfig(specs []model.ToolSpec) *wireToolConfig {
	if len(specs) == 0 {
		return nil
	}
	declarations := make([]wireToolDeclaration, 0, len(specs))
	for _, spec := range specs {
		var declaration wireToolDeclaration
		declaration.ToolSpec.Name = spec.Name
		declaration.ToolSpec.Description = spec.Description
		declaration.ToolSpec.InputSchema = wireInputSchema{JSON: spec.Schema}
		declarations = append(declarations, declaration)
	}
	return &wireToolConfig{Tools: declarations}
}

// normalizeInput ensures tool inputs are JSON objects: an empty value maps
// to an empty object, matching the wire contract.
func normalizeInput(input json.RawMessage) json.RawMessage {
	trimmed := strings.TrimSpace(string(input))
	if trimmed == "" {
		return json.RawMessage("{}")
	}
	return json.RawMessage(trimmed)
}

// fromWireResponse normalizes a Converse body: text blocks join into the
// message content, reasoningContent blocks keep their order and
// signatures, and toolUse blocks become requested calls with their
// provider IDs. Tool calls decide the turn, so a response with both keeps
// its calls and its text as evidence.
func fromWireResponse(payload []byte) (model.Response, error) {
	var wire converseResponse
	if err := json.Unmarshal(payload, &wire); err != nil {
		return model.Response{}, &DecodeError{Stage: "decode response body", Err: err}
	}

	var texts []string
	var calls []model.ToolCall
	var thinking []model.ThinkingBlock
	for _, block := range wire.Output.Message.Content {
		switch {
		case block.Text != "":
			texts = append(texts, block.Text)
		case block.ReasoningContent != nil:
			if reasoning := block.ReasoningContent; reasoning.RedactedContent != "" {
				thinking = append(thinking, model.ThinkingRedacted(reasoning.RedactedContent))
			} else if reasoning.ReasoningText != nil {
				thinking = append(thinking, model.ThinkingSigned(
					reasoning.ReasoningText.Text, reasoning.ReasoningText.Signature))
			}
		case block.ToolUse != nil:
			calls = append(calls, model.ToolCall{
				ID:   block.ToolUse.ToolUseID,
				Name: block.ToolUse.Name,
				Args: normalizeInput(block.ToolUse.Input),
			})
		}
	}

	return model.Response{
		Message: model.Message{
			Role:      model.RoleAssistant,
			Content:   strings.Join(texts, "\n"),
			ToolCalls: calls,
			Thinking:  thinking,
		},
		Usage: model.Usage{
			InputTokens:  wire.Usage.InputTokens,
			OutputTokens: wire.Usage.OutputTokens,
		},
		FinishReason: finishReason(wire.StopReason),
	}, nil
}

// finishReason translates the Converse API stopReason vocabulary onto the
// shared model constants. end_turn and a hit stop sequence are a natural
// completion; guardrail and content-filter interventions are safety
// stops.
func finishReason(reason string) model.FinishReason {
	switch reason {
	case "end_turn", "stop_sequence":
		return model.FinishStop
	case "max_tokens":
		return model.FinishLength
	case "tool_use":
		return model.FinishToolCall
	case "guardrail_intervened", "content_filtered":
		return model.FinishContentFilter
	case "":
		return ""
	default:
		return model.FinishOther
	}
}
