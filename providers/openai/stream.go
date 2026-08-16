package openai

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/abubakarsiddik31/golem/model"
)

// doneSentinel terminates an OpenAI-compatible SSE stream.
const doneSentinel = "[DONE]"

// maxSSELine bounds one SSE line. Streamed tool arguments arrive as JSON
// text inside a single data line, which can exceed the bufio.Scanner
// default of 64 KB.
const maxSSELine = 1 << 20

// Client streams responses through the same chat-completions endpoint as
// Generate.
var _ model.StreamingModel = (*Client)(nil)

// GenerateStream translates request to the chat-completions wire format
// with streaming enabled, reports fragments to onDelta as they arrive, and
// returns the fully assembled response — the same normalization Generate
// would produce. Error classification is identical to Generate. A non-nil
// return from onDelta stops the stream and is returned as-is.
func (c *Client) GenerateStream(ctx context.Context, request model.Request, onDelta func(model.Delta) error) (model.Response, error) {
	httpRequest, err := c.newChatHTTPRequest(ctx, request, true)
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
// returns the assembled response. The [DONE] sentinel is required: EOF
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
			continue // comments, event lines, keep-alives
		}
		if data == doneSentinel {
			done = true
			break
		}
		if err := assembler.consume(data, onDelta); err != nil {
			return model.Response{}, err
		}
	}
	if err := scanner.Err(); err != nil {
		return model.Response{}, &TransportError{Err: err}
	}
	if !done {
		return model.Response{}, &DecodeError{
			Stage: "decode stream",
			Err:   fmt.Errorf("stream ended without [DONE] sentinel"),
		}
	}
	return assembler.response(), nil
}

// sseData returns the payload of a data: line. Every other line —
// comments, event names, blanks — reports ok=false.
func sseData(line string) (string, bool) {
	after, ok := strings.CutPrefix(line, "data:")
	if !ok {
		return "", false
	}
	return strings.TrimSpace(after), true
}

// streamAssembler accumulates chunk fragments into the final response.
// Index-based tool-call merging is wire-specific and stays in
// the adapter.
type streamAssembler struct {
	content strings.Builder
	calls   []model.ToolCall
	args    []strings.Builder
	usage   model.Usage
}

// consume decodes one data payload, accumulates it, and reports the
// fragment to onDelta. The first choice wins; a chunk with
// no choices carries usage only.
func (a *streamAssembler) consume(data string, onDelta func(model.Delta) error) error {
	var chunk chatChunk
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return &DecodeError{Stage: "decode stream chunk", Err: err}
	}
	if chunk.Usage != nil {
		a.usage = model.Usage{
			InputTokens:  chunk.Usage.PromptTokens,
			OutputTokens: chunk.Usage.CompletionTokens,
		}
	}
	if len(chunk.Choices) == 0 {
		return nil
	}

	var delta model.Delta
	message := chunk.Choices[0].Delta
	if message.Content != "" {
		a.content.WriteString(message.Content)
		delta.Content = message.Content
	}
	for _, call := range message.ToolCalls {
		delta.ToolCalls = append(delta.ToolCalls, a.toolCallFragment(call))
	}
	if onDelta != nil && (delta.Content != "" || len(delta.ToolCalls) > 0) {
		if err := onDelta(delta); err != nil {
			return err
		}
	}
	return nil
}

// toolCallFragment merges one wire fragment into the accumulated calls and
// returns its port-level form. ID and Name are reported only on the first
// fragment of their call.
func (a *streamAssembler) toolCallFragment(call chatToolCallDelta) model.ToolCallDelta {
	for call.Index >= len(a.calls) {
		a.calls = append(a.calls, model.ToolCall{})
		a.args = append(a.args, strings.Builder{})
	}
	fragment := model.ToolCallDelta{
		Index:        call.Index,
		ArgsFragment: call.Function.Arguments,
	}
	if call.ID != "" {
		fragment.ID = call.ID
		a.calls[call.Index].ID = call.ID
	}
	if call.Function.Name != "" {
		fragment.Name = call.Function.Name
		a.calls[call.Index].Name = call.Function.Name
	}
	a.args[call.Index].WriteString(call.Function.Arguments)
	return fragment
}

// response assembles the final normalized response.
func (a *streamAssembler) response() model.Response {
	calls := make([]model.ToolCall, len(a.calls))
	for i, call := range a.calls {
		call.Args = normalizeArguments(a.args[i].String())
		calls[i] = call
	}
	if len(calls) == 0 {
		calls = nil
	}
	return model.Response{
		Message: model.Message{
			Role:      model.RoleAssistant,
			Content:   a.content.String(),
			ToolCalls: calls,
		},
		Usage: a.usage,
	}
}
