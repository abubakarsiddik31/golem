package gemini

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/abubakarsiddik31/golem/model"
)

// maxSSELine bounds one SSE line. Streamed arguments arrive as JSON text
// inside a single data line, which can exceed the bufio.Scanner default
// of 64 KB.
const maxSSELine = 1 << 20

// Client streams responses through the same models endpoint as Generate.
var _ model.StreamingModel = (*Client)(nil)

// GenerateStream translates request to the GenerateContent wire format
// with streaming enabled, reports fragments to onDelta as they arrive,
// and returns the fully assembled response — the same normalization
// Generate would produce. Error classification is identical to Generate.
// A non-nil return from onDelta stops the stream and is returned as-is.
//
// The Gemini stream carries no terminal sentinel: it ends at EOF, so a
// truncated stream cannot be distinguished from a short complete one the
// way the OpenAI-compatible and Anthropic adapters do.
func (c *Client) GenerateStream(ctx context.Context, request model.Request, onDelta func(model.Delta) error) (model.Response, error) {
	httpRequest, err := c.newGenerateContentHTTPRequest(ctx, request, true)
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
// returns the assembled response. Each data line is a complete
// GenerateContentResponse chunk; text parts are forwarded as they arrive,
// and function calls — which arrive whole — are forwarded as single
// fragments with their complete arguments.
func readStream(body io.Reader, onDelta func(model.Delta) error) (model.Response, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxSSELine)

	var assembler streamAssembler
	for scanner.Scan() {
		data, ok := sseData(scanner.Text())
		if !ok {
			continue // comments, event lines, keep-alives
		}
		if err := assembler.consume(data, onDelta); err != nil {
			return model.Response{}, err
		}
	}
	if err := scanner.Err(); err != nil {
		return model.Response{}, &TransportError{Err: err}
	}
	return assembler.response()
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

// streamAssembler accumulates chunks into the final response. Call IDs
// are generated across the whole stream so they stay unique.
type streamAssembler struct {
	content strings.Builder
	calls   []model.ToolCall
	usage   model.Usage
}

// consume decodes one chunk payload, accumulates it, and reports its
// fragments to onDelta.
func (a *streamAssembler) consume(data string, onDelta func(model.Delta) error) error {
	var chunk generateContentResponse
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return &DecodeError{Stage: "decode stream chunk", Err: err}
	}
	if chunk.Usage.PromptTokens > 0 || chunk.Usage.CandidatesTokens > 0 {
		a.usage.InputTokens = chunk.Usage.PromptTokens
		a.usage.OutputTokens = chunk.Usage.CandidatesTokens
	}
	if len(chunk.Candidates) == 0 {
		return nil
	}

	var delta model.Delta
	for _, part := range chunk.Candidates[0].Content.Parts {
		switch {
		case part.Text != "":
			a.content.WriteString(part.Text)
			delta.Content = part.Text
		case part.FunctionCall != nil:
			call := model.ToolCall{
				ID:   fmt.Sprintf("call-%d", len(a.calls)+1),
				Name: part.FunctionCall.Name,
				Args: normalizeArgs(part.FunctionCall.Args),
			}
			a.calls = append(a.calls, call)
			delta.ToolCalls = append(delta.ToolCalls, model.ToolCallDelta{
				Index:        len(a.calls) - 1,
				ID:           call.ID,
				Name:         call.Name,
				ArgsFragment: string(call.Args),
			})
		}
	}
	if onDelta != nil && (delta.Content != "" || len(delta.ToolCalls) > 0) {
		if err := onDelta(delta); err != nil {
			return err
		}
	}
	return nil
}

// response assembles the final normalized response.
func (a *streamAssembler) response() (model.Response, error) {
	calls := a.calls
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
	}, nil
}
