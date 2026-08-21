package anthropic

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/abubakarsiddik31/golem/model"
)

// messagesRequest is the Messages API wire request. Field names follow the
// provider API; omitempty keeps absent system guidance and tools off the
// wire.
type messagesRequest struct {
	Model     string        `json:"model"`
	MaxTokens int           `json:"max_tokens"`
	System    string        `json:"system,omitempty"`
	Messages  []wireMessage `json:"messages"`
	Tools     []wireTool    `json:"tools,omitempty"`
	// Sampling controls; unset fields stay off the wire.
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	// OutputConfig requests provider-enforced output structure; set for
	// structured output.
	OutputConfig *wireOutputConfig `json:"output_config,omitempty"`
	// Stream selects streaming mode.
	Stream bool `json:"stream,omitempty"`
}

// wireOutputConfig is the structured-output configuration of a request.
// The json_schema format requires a strict-conformant schema from the
// caller; the provider rejects non-conformant schemas with an API error
// instead of the adapter silently relaxing them.
type wireOutputConfig struct {
	Format *wireOutputFormat `json:"format,omitempty"`
}

type wireOutputFormat struct {
	Type   string          `json:"type"`
	Schema json.RawMessage `json:"schema"`
}

// wireMessage is one conversational turn. The Messages API alternates user
// and assistant turns; tool results travel as user-turn blocks.
type wireMessage struct {
	Role    string      `json:"role"`
	Content []wireBlock `json:"content"`
}

// wireBlock is one content block of a turn. Text blocks carry prose,
// tool_use blocks carry a model-requested execution on assistant turns,
// and tool_result blocks carry the execution outcome back on user turns.
type wireBlock struct {
	Type string `json:"type"`
	// Text is the block text for type "text".
	Text string `json:"text,omitempty"`
	// ID, Name, and Input identify the requested execution for type
	// "tool_use". Input is a JSON object.
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// ToolUseID correlates a "tool_result" block with its requested call;
	// Result carries the outcome handed back to the model.
	ToolUseID string `json:"tool_use_id,omitempty"`
	Result    string `json:"content,omitempty"`
	// Source carries an "image" block's payload: a provider-fetched URL
	// or base64 inline data with its media type.
	Source *wireSource `json:"source,omitempty"`
}

// wireSource is an image block source: a URL the provider fetches, or
// base64 inline data with its media type.
type wireSource struct {
	Type      string `json:"type"`
	URL       string `json:"url,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
}

// wireTool advertises one tool to the model.
type wireTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// messagesResponse is the Messages API wire response.
type messagesResponse struct {
	Content []wireBlock `json:"content"`
	Usage   wireUsage   `json:"usage"`
}

type wireUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// toWireTurns converts normalized messages into Messages API turns.
// System messages become top-level system guidance; consecutive messages
// of the same role — tool results after an assistant turn, or a user
// prompt after tool results — merge into one turn, because the API expects
// alternating roles with tool results as user-turn blocks.
func toWireTurns(messages []model.Message) (system string, turns []wireMessage) {
	var systemParts []string
	for _, message := range messages {
		var role string
		var blocks []wireBlock
		switch message.Role {
		case model.RoleSystem:
			systemParts = append(systemParts, message.Content)
			continue
		case model.RoleAssistant:
			role = "assistant"
			blocks = assistantBlocks(message)
		case model.RoleTool:
			role = "user"
			blocks = []wireBlock{{
				Type:      "tool_result",
				ToolUseID: message.ToolCallID,
				Result:    message.Content,
			}}
		default:
			role = "user"
			blocks = userBlocks(message)
		}
		if len(turns) > 0 && turns[len(turns)-1].Role == role {
			turns[len(turns)-1].Content = append(turns[len(turns)-1].Content, blocks...)
			continue
		}
		turns = append(turns, wireMessage{Role: role, Content: blocks})
	}
	return strings.Join(systemParts, "\n\n"), turns
}

// assistantBlocks renders an assistant message: its text as a text block,
// when present, followed by one tool_use block per requested call.
func assistantBlocks(message model.Message) []wireBlock {
	var blocks []wireBlock
	if message.Content != "" {
		blocks = append(blocks, wireBlock{Type: "text", Text: message.Content})
	}
	for _, call := range message.ToolCalls {
		blocks = append(blocks, wireBlock{
			Type:  "tool_use",
			ID:    call.ID,
			Name:  call.Name,
			Input: normalizeInput(call.Args),
		})
	}
	return blocks
}

// userBlocks renders a user message: its text as a text block when
// present, followed by one image block per attached part. URL parts send
// a provider-fetched source; inline data sends base64.
func userBlocks(message model.Message) []wireBlock {
	var blocks []wireBlock
	if message.Content != "" {
		blocks = append(blocks, wireBlock{Type: "text", Text: message.Content})
	}
	for _, part := range message.Parts {
		if part.URL != "" {
			blocks = append(blocks, wireBlock{
				Type:   "image",
				Source: &wireSource{Type: "url", URL: part.URL},
			})
			continue
		}
		blocks = append(blocks, wireBlock{
			Type: "image",
			Source: &wireSource{
				Type:      "base64",
				MediaType: part.MediaType,
				Data:      base64.StdEncoding.EncodeToString(part.Data),
			},
		})
	}
	if len(blocks) == 0 {
		blocks = []wireBlock{{Type: "text", Text: message.Content}}
	}
	return blocks
}

func toWireTools(specs []model.ToolSpec) []wireTool {
	if len(specs) == 0 {
		return nil
	}
	wire := make([]wireTool, 0, len(specs))
	for _, spec := range specs {
		wire = append(wire, wireTool{
			Name:        spec.Name,
			Description: spec.Description,
			InputSchema: spec.Schema,
		})
	}
	return wire
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

// fromWireResponse normalizes a Messages API body: text blocks join into
// the message content, tool_use blocks become requested calls, and
// unknown block types are skipped. Tool calls decide the turn, so a
// response with both keeps its calls and its text as evidence.
func fromWireResponse(payload []byte) (model.Response, error) {
	var wire messagesResponse
	if err := json.Unmarshal(payload, &wire); err != nil {
		return model.Response{}, &DecodeError{Stage: "decode response body", Err: err}
	}

	var texts []string
	var calls []model.ToolCall
	for _, block := range wire.Content {
		switch block.Type {
		case "text":
			texts = append(texts, block.Text)
		case "tool_use":
			calls = append(calls, model.ToolCall{
				ID:   block.ID,
				Name: block.Name,
				Args: normalizeInput(block.Input),
			})
		}
	}

	return model.Response{
		Message: model.Message{
			Role:      model.RoleAssistant,
			Content:   strings.Join(texts, "\n"),
			ToolCalls: calls,
		},
		Usage: model.Usage{
			InputTokens:  wire.Usage.InputTokens,
			OutputTokens: wire.Usage.OutputTokens,
		},
	}, nil
}
