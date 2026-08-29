package anthropic

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/abubakarsiddik31/golem/model"
)

// maxSSELine bounds one SSE line. Streamed tool inputs arrive as JSON
// text inside a single data line, which can exceed the bufio.Scanner
// default of 64 KB.
const maxSSELine = 1 << 20

// Client streams responses through the same messages endpoint as
// Generate.
var _ model.StreamingModel = (*Client)(nil)

// GenerateStream translates request to the Messages API wire format with
// streaming enabled, reports fragments to onDelta as they arrive, and
// returns the fully assembled response — the same normalization Generate
// would produce. Error classification is identical to Generate, with one
// addition: provider error events mid-stream classify as *APIError. A
// non-nil return from onDelta stops the stream and is returned as-is.
func (c *Client) GenerateStream(ctx context.Context, request model.Request, onDelta func(model.Delta) error) (model.Response, error) {
	httpRequest, err := c.newMessagesHTTPRequest(ctx, request, true)
	if err != nil {
		return model.Response{}, err
	}

	httpResponse, err := c.http.Do(httpRequest)
	if err != nil {
		return model.Response{}, &TransportError{Err: err}
	}
	defer httpResponse.Body.Close()

	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		payload, readErr := io.ReadAll(httpResponse.Body)
		if readErr != nil {
			return model.Response{}, &TransportError{Err: readErr}
		}
		return model.Response{}, newAPIError(httpResponse.StatusCode, payload)
	}

	return readStream(httpResponse.Body, onDelta)
}

// readStream parses the SSE body, forwards fragments to onDelta, and
// returns the assembled response. The message_stop event is required: EOF
// without it means the stream was truncated, which is a decode error, not
// a short success.
func readStream(body io.Reader, onDelta func(model.Delta) error) (model.Response, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxSSELine)

	var assembler streamAssembler
	done := false
	for scanner.Scan() {
		data, ok := sseData(scanner.Text())
		if !ok {
			continue // comments, event lines, pings, keep-alives
		}
		finished, err := assembler.consume(data, onDelta)
		if err != nil {
			return model.Response{}, err
		}
		if finished {
			done = true
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return model.Response{}, &TransportError{Err: err}
	}
	if !done {
		return model.Response{}, &DecodeError{
			Stage: "decode stream",
			Err:   fmt.Errorf("stream ended without a message_stop event"),
		}
	}
	response, err := assembler.response()
	if err != nil {
		return model.Response{}, err
	}
	return response, nil
}

// sseData returns the payload of a data: line. Every other line —
// comments, event names, blanks — reports ok=false. Each Anthropic data
// payload carries its own type field, so event lines carry no extra
// information.
func sseData(line string) (string, bool) {
	after, ok := strings.CutPrefix(line, "data:")
	if !ok {
		return "", false
	}
	return strings.TrimSpace(after), true
}

// streamEvent is one Messages API streaming event. Fields are populated
// per event type; the rest stay zero.
type streamEvent struct {
	Type string `json:"type"`
	// Message carries the message snapshot on message_start; only its
	// usage matters here.
	Message struct {
		Usage wireUsage `json:"usage"`
	} `json:"message"`
	// Index identifies the content block a block event belongs to.
	Index        int               `json:"index"`
	ContentBlock *wireBlock        `json:"content_block"`
	Delta        *streamEventDelta `json:"delta"`
	// Usage carries cumulative usage on message_delta.
	Usage *wireUsage `json:"usage"`
	// Error carries a provider failure raised mid-stream.
	Error *errorBody `json:"error"`
}

// streamEventDelta is one content fragment.
type streamEventDelta struct {
	Type string `json:"type"` // "text_delta" | "thinking_delta" | "signature_delta" | "input_json_delta"
	Text string `json:"text,omitempty"`
	// Thinking is a fragment of the block's reasoning text on
	// thinking_delta events.
	Thinking string `json:"thinking,omitempty"`
	// Signature is the block's verification token, which arrives whole in
	// one event, on signature_delta events.
	Signature string `json:"signature,omitempty"`
	// PartialJSON is a fragment of the block's tool input.
	PartialJSON string `json:"partial_json,omitempty"`
}

// streamAssembler accumulates event fragments into the final response.
// Block-index-to-call mapping is wire-specific and stays in the adapter.
type streamAssembler struct {
	content strings.Builder
	// ordinals maps wire content-block indexes to tool-call ordinals, so
	// emitted fragments correlate with the assembled calls.
	ordinals map[int]int
	calls    []model.ToolCall
	args     []strings.Builder
	// identified marks calls whose first fragment — the one carrying ID
	// and Name — has already been emitted.
	identified []bool
	// thinkingOrdinals maps wire content-block indexes to thinking-block
	// ordinals the same way; thinkingText accumulates each visible block's
	// reasoning in stream order, including interleaved blocks.
	thinkingOrdinals map[int]int
	thinking         []model.ThinkingBlock
	thinkingText     []strings.Builder
	usage            model.Usage
}

// consume decodes one event payload, accumulates it, reports fragments to
// onDelta, and reports whether the stream finished. Unknown event types
// are skipped: the provider may add events without breaking the adapter.
func (a *streamAssembler) consume(data string, onDelta func(model.Delta) error) (bool, error) {
	var event streamEvent
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return false, &DecodeError{Stage: "decode stream event", Err: err}
	}

	switch event.Type {
	case "message_start":
		a.usage.InputTokens = event.Message.Usage.InputTokens
		a.usage.OutputTokens = event.Message.Usage.OutputTokens
	case "content_block_start":
		if event.ContentBlock == nil {
			return false, nil
		}
		switch event.ContentBlock.Type {
		case "tool_use":
			if a.ordinals == nil {
				a.ordinals = make(map[int]int)
			}
			a.ordinals[event.Index] = len(a.calls)
			a.calls = append(a.calls, model.ToolCall{
				ID:   event.ContentBlock.ID,
				Name: event.ContentBlock.Name,
			})
			a.args = append(a.args, strings.Builder{})
			a.identified = append(a.identified, false)
		case "thinking":
			if a.thinkingOrdinals == nil {
				a.thinkingOrdinals = make(map[int]int)
			}
			a.thinkingOrdinals[event.Index] = len(a.thinking)
			a.thinking = append(a.thinking, model.ThinkingText(event.ContentBlock.Thinking))
			a.thinkingText = append(a.thinkingText, strings.Builder{})
		case "redacted_thinking":
			// Redacted reasoning arrives whole; it has no deltas.
			a.thinking = append(a.thinking, model.ThinkingRedacted(event.ContentBlock.Redacted))
		}
	case "content_block_delta":
		if event.Delta == nil {
			return false, nil
		}
		switch event.Delta.Type {
		case "text_delta":
			a.content.WriteString(event.Delta.Text)
			if err := emit(onDelta, model.Delta{Content: event.Delta.Text}); err != nil {
				return false, err
			}
		case "thinking_delta":
			ordinal, ok := a.thinkingOrdinals[event.Index]
			if !ok {
				return false, &DecodeError{
					Stage: "decode stream event",
					Err:   fmt.Errorf("thinking fragment for block %d, which is not a thinking block", event.Index),
				}
			}
			a.thinkingText[ordinal].WriteString(event.Delta.Thinking)
			if err := emit(onDelta, model.Delta{Thinking: []model.ThinkingDelta{{
				Index: ordinal,
				Text:  event.Delta.Thinking,
			}}}); err != nil {
				return false, err
			}
		case "signature_delta":
			ordinal, ok := a.thinkingOrdinals[event.Index]
			if !ok {
				return false, &DecodeError{
					Stage: "decode stream event",
					Err:   fmt.Errorf("signature fragment for block %d, which is not a thinking block", event.Index),
				}
			}
			a.thinking[ordinal].Signature = event.Delta.Signature
			if err := emit(onDelta, model.Delta{Thinking: []model.ThinkingDelta{{
				Index:     ordinal,
				Signature: event.Delta.Signature,
			}}}); err != nil {
				return false, err
			}
		case "input_json_delta":
			ordinal, ok := a.ordinals[event.Index]
			if !ok {
				return false, &DecodeError{
					Stage: "decode stream event",
					Err:   fmt.Errorf("input fragment for block %d, which is not a tool_use block", event.Index),
				}
			}
			fragment := model.ToolCallDelta{
				Index:        ordinal,
				ArgsFragment: event.Delta.PartialJSON,
			}
			if !a.identified[ordinal] {
				fragment.ID = a.calls[ordinal].ID
				fragment.Name = a.calls[ordinal].Name
				a.identified[ordinal] = true
			}
			a.args[ordinal].WriteString(event.Delta.PartialJSON)
			if err := emit(onDelta, model.Delta{ToolCalls: []model.ToolCallDelta{fragment}}); err != nil {
				return false, err
			}
		}
	case "message_delta":
		if event.Usage != nil {
			a.usage.OutputTokens = event.Usage.OutputTokens
		}
	case "message_stop":
		return true, nil
	case "error":
		if event.Error != nil {
			return false, newStreamError(event.Error)
		}
	}
	return false, nil
}

func emit(onDelta func(model.Delta) error, delta model.Delta) error {
	if onDelta == nil {
		return nil
	}
	return onDelta(delta)
}

// newStreamError classifies a provider error event. The wire body carries
// no HTTP status, so known transient types classify directly.
func newStreamError(body *errorBody) *APIError {
	apiError := &APIError{
		Code:    body.Type,
		Message: body.Message,
	}
	switch body.Type {
	case "overloaded_error", "rate_limit_error", "api_error":
		apiError.retryable = true
	}
	return apiError
}

// response assembles the final normalized response. Tool inputs are
// validated here: the provider guarantees JSON objects, so anything else
// is a truncated or corrupt stream.
func (a *streamAssembler) response() (model.Response, error) {
	calls := make([]model.ToolCall, len(a.calls))
	for i, call := range a.calls {
		args := normalizeInput(json.RawMessage(a.args[i].String()))
		if !json.Valid(args) {
			return model.Response{}, &DecodeError{
				Stage: "decode stream",
				Err:   fmt.Errorf("tool %q input is not valid JSON: %s", call.Name, a.args[i].String()),
			}
		}
		call.Args = args
		calls[i] = call
	}
	if len(calls) == 0 {
		calls = nil
	}
	thinking := a.thinking
	for i := range thinking {
		if thinking[i].Redacted != "" {
			continue
		}
		thinking[i].Text = a.thinkingText[i].String()
	}
	if len(thinking) == 0 {
		thinking = nil
	}
	return model.Response{
		Message: model.Message{
			Role:      model.RoleAssistant,
			Content:   a.content.String(),
			ToolCalls: calls,
			Thinking:  thinking,
		},
		Usage: a.usage,
	}, nil
}
