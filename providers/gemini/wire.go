package gemini

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/abubakarsiddik31/golem/model"
)

// generateContentRequest is the GenerateContent API wire request. Field
// names follow the provider API; omitempty keeps absent system guidance,
// tools, and generation config off the wire.
type generateContentRequest struct {
	Contents          []wireContent  `json:"contents"`
	SystemInstruction *wireSystem    `json:"systemInstruction,omitempty"`
	Tools             []wireToolList `json:"tools,omitempty"`
	GenerationConfig  *wireGenConfig `json:"generationConfig,omitempty"`
}

// wireContent is one conversational turn. Roles alternate between "user"
// and "model"; function responses travel as user-turn parts.
type wireContent struct {
	Role  string     `json:"role"`
	Parts []wirePart `json:"parts"`
}

// wirePart is one part of a turn: text, a model-requested function call,
// or the outcome of one. Function calls carry no provider ID; the adapter
// generates stable ones and correlates responses by tool name.
type wirePart struct {
	Text string `json:"text,omitempty"`
	// FunctionCall carries a model-requested execution on model turns.
	FunctionCall *wireFunctionCall `json:"functionCall,omitempty"`
	// FunctionResponse carries the outcome back on user turns. The
	// response field must be an object; text results wrap as
	// {"result": string}.
	FunctionResponse *wireFunctionResponse `json:"functionResponse,omitempty"`
	// InlineData carries inline binary content on user turns, such as
	// image bytes with their media type.
	InlineData *wireInlineData `json:"inlineData,omitempty"`
	// FileData carries provider-addressable content by URI on user turns,
	// such as a Files API or GCS image.
	FileData *wireFileData `json:"fileData,omitempty"`
}

// wireInlineData is base64 inline content. The GenerateContent API only
// accepts bytes the API can decode itself; arbitrary remote URLs must be
// fetched by the application and sent inline.
type wireInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

// wireFileData references content by URI. GenerateContent resolves URIs
// the provider can reach itself, such as Files API or GCS objects.
type wireFileData struct {
	FileURI string `json:"fileUri"`
}

type wireFunctionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type wireFunctionResponse struct {
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
}

// wireSystem is top-level system guidance.
type wireSystem struct {
	Parts []wirePart `json:"parts"`
}

// wireToolList declares tools for the request. Golem sends at most one
// list of function declarations.
type wireToolList struct {
	FunctionDeclarations []wireFunctionDecl `json:"functionDeclarations"`
}

type wireFunctionDecl struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// wireGenConfig shapes generation. A request carrying an output schema
// also selects JSON responses.
type wireGenConfig struct {
	ResponseMimeType string          `json:"responseMimeType,omitempty"`
	ResponseSchema   json.RawMessage `json:"responseSchema,omitempty"`
	// Sampling and length controls; unset fields stay off the wire.
	Temperature     *float64 `json:"temperature,omitempty"`
	TopP            *float64 `json:"topP,omitempty"`
	MaxOutputTokens int      `json:"maxOutputTokens,omitempty"`
}

// generateContentResponse is the GenerateContent API wire response.
type generateContentResponse struct {
	Candidates []wireCandidate `json:"candidates"`
	Usage      wireUsageMeta   `json:"usageMetadata"`
}

type wireCandidate struct {
	Content      wireContent `json:"content"`
	FinishReason string      `json:"finishReason"`
}

type wireUsageMeta struct {
	PromptTokens     int `json:"promptTokenCount"`
	CandidatesTokens int `json:"candidatesTokenCount"`
}

// toWireContents converts normalized messages into GenerateContent turns.
// System messages become top-level system guidance; consecutive messages
// of the same role — function responses after a model turn, or a user
// prompt after function responses — merge into one turn, because parts of
// one turn share its role.
func toWireContents(messages []model.Message) (system *wireSystem, contents []wireContent) {
	var systemParts []wirePart
	for _, message := range messages {
		var role string
		var parts []wirePart
		switch message.Role {
		case model.RoleSystem:
			systemParts = append(systemParts, wirePart{Text: message.Content})
			continue
		case model.RoleAssistant:
			role = "model"
			parts = modelParts(message)
		case model.RoleTool:
			role = "user"
			parts = []wirePart{{FunctionResponse: &wireFunctionResponse{
				Name:     message.ToolName,
				Response: json.RawMessage(`{"result":` + quoteJSON(message.Content) + `}`),
			}}}
		default:
			role = "user"
			parts = userParts(message)
		}
		if len(contents) > 0 && contents[len(contents)-1].Role == role {
			contents[len(contents)-1].Parts = append(contents[len(contents)-1].Parts, parts...)
			continue
		}
		contents = append(contents, wireContent{Role: role, Parts: parts})
	}
	if len(systemParts) > 0 {
		system = &wireSystem{Parts: systemParts}
	}
	return system, contents
}

// modelParts renders an assistant message: its text as a text part, when
// present, followed by one function call part per requested call.
func modelParts(message model.Message) []wirePart {
	var parts []wirePart
	if message.Content != "" {
		parts = append(parts, wirePart{Text: message.Content})
	}
	for _, call := range message.ToolCalls {
		parts = append(parts, wirePart{FunctionCall: &wireFunctionCall{
			Name: call.Name,
			Args: normalizeArgs(call.Args),
		}})
	}
	return parts
}

// userParts renders a user message: its text as a text part when present,
// followed by one image part per attached part. Inline bytes send base64
// inlineData; URLs send fileData and must reference content the provider
// can reach itself, such as Files API or GCS objects.
func userParts(message model.Message) []wirePart {
	var parts []wirePart
	if message.Content != "" {
		parts = append(parts, wirePart{Text: message.Content})
	}
	for _, part := range message.Parts {
		if part.URL != "" {
			parts = append(parts, wirePart{FileData: &wireFileData{FileURI: part.URL}})
			continue
		}
		parts = append(parts, wirePart{InlineData: &wireInlineData{
			MimeType: part.MediaType,
			Data:     base64.StdEncoding.EncodeToString(part.Data),
		}})
	}
	if len(parts) == 0 {
		parts = []wirePart{{Text: message.Content}}
	}
	return parts
}

func toWireTools(specs []model.ToolSpec) []wireToolList {
	if len(specs) == 0 {
		return nil
	}
	declarations := make([]wireFunctionDecl, 0, len(specs))
	for _, spec := range specs {
		declarations = append(declarations, wireFunctionDecl{
			Name:        spec.Name,
			Description: spec.Description,
			Parameters:  spec.Schema,
		})
	}
	return []wireToolList{{FunctionDeclarations: declarations}}
}

// normalizeArgs ensures function arguments are JSON objects: an empty
// value maps to an empty object, matching the wire contract.
func normalizeArgs(args json.RawMessage) json.RawMessage {
	trimmed := strings.TrimSpace(string(args))
	if trimmed == "" {
		return json.RawMessage("{}")
	}
	return json.RawMessage(trimmed)
}

// quoteJSON renders s as a JSON string literal.
func quoteJSON(s string) string {
	encoded, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(encoded)
}

// fromWireResponse normalizes a GenerateContent body: text parts join
// into the message content, function call parts become requested calls
// with adapter-generated stable IDs, and the first candidate wins. Tool
// calls decide the turn, so a response with both keeps its calls and its
// text as evidence.
func fromWireResponse(payload []byte) (model.Response, error) {
	var wire generateContentResponse
	if err := json.Unmarshal(payload, &wire); err != nil {
		return model.Response{}, &DecodeError{Stage: "decode response body", Err: err}
	}
	if len(wire.Candidates) == 0 {
		return model.Response{}, &DecodeError{
			Stage: "decode response body",
			Err:   fmt.Errorf("response contained no candidates"),
		}
	}
	return assembleResponse(&wire)
}

// assembleResponse turns one wire response into the normalized form.
// callSeq makes generated call IDs unique within a stream.
func assembleResponse(wire *generateContentResponse) (model.Response, error) {
	var texts []string
	var calls []model.ToolCall
	callSeq := 0
	for _, part := range wire.Candidates[0].Content.Parts {
		switch {
		case part.Text != "":
			texts = append(texts, part.Text)
		case part.FunctionCall != nil:
			callSeq++
			calls = append(calls, model.ToolCall{
				ID:   fmt.Sprintf("call-%d", callSeq),
				Name: part.FunctionCall.Name,
				Args: normalizeArgs(part.FunctionCall.Args),
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
			InputTokens:  wire.Usage.PromptTokens,
			OutputTokens: wire.Usage.CandidatesTokens,
		},
	}, nil
}
