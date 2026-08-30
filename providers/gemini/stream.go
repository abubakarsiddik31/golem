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
// The Gemini stream carries no protocol-level sentinel such as the
// OpenAI-compatible [DONE] line or Anthropic's message_stop event: the
// terminal signal is the final chunk's finishReason. A stream that ends
// without one was truncated in transit, which fails as a decode error
// rather than passing as a short complete answer.
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
// fragments with their complete arguments. A terminal finishReason on any
// candidate chunk is required: EOF without one means the stream was
// truncated in transit, which is a decode error, not a short success.
func readStream(body io.Reader, onDelta func(model.Delta) error) (model.Response, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxSSELine)

	assembler := streamAssembler{currentThinking: -1}
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
	if !assembler.finished {
		return model.Response{}, &DecodeError{
			Stage: "decode stream",
			Err:   fmt.Errorf("stream ended without a terminal finishReason"),
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

// streamAssembler accumulates chunks into the final response. Call IDs
// are generated across the whole stream so they stay unique. Contiguous
// thought parts join into one thinking block; a non-thought part starts a
// fresh block, mirroring how the provider interleaves reasoning with
// content.
type streamAssembler struct {
	content strings.Builder
	calls   []model.ToolCall
	usage   model.Usage
	// finished records that a candidate chunk carried a finishReason —
	// the stream's only terminal signal.
	finished bool
	// thinking holds assembled reasoning blocks; currentThinking is the
	// index of the block that continues thought text, -1 when the next
	// thought part opens a new block.
	thinking        []model.ThinkingBlock
	currentThinking int
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
	if chunk.Candidates[0].FinishReason != "" {
		a.finished = true
	}

	var delta model.Delta
	for _, part := range chunk.Candidates[0].Content.Parts {
		switch {
		case part.Thought:
			if a.currentThinking < 0 {
				a.currentThinking = len(a.thinking)
				a.thinking = append(a.thinking, model.ThinkingText(""))
			}
			a.thinking[a.currentThinking].Text += part.Text
			if part.ThoughtSignature != "" {
				a.thinking[a.currentThinking].Signature = part.ThoughtSignature
			}
			delta.Thinking = append(delta.Thinking, model.ThinkingDelta{
				Index:     a.currentThinking,
				Text:      part.Text,
				Signature: part.ThoughtSignature,
			})
		case part.Text != "":
			a.content.WriteString(part.Text)
			delta.Content = part.Text
			a.currentThinking = -1
		case part.FunctionCall != nil:
			call := model.ToolCall{
				ID:        fmt.Sprintf("call-%d", len(a.calls)+1),
				Name:      part.FunctionCall.Name,
				Args:      normalizeArgs(part.FunctionCall.Args),
				Signature: part.ThoughtSignature,
			}
			a.calls = append(a.calls, call)
			delta.ToolCalls = append(delta.ToolCalls, model.ToolCallDelta{
				Index:        len(a.calls) - 1,
				ID:           call.ID,
				Name:         call.Name,
				ArgsFragment: string(call.Args),
				Signature:    part.ThoughtSignature,
			})
			a.currentThinking = -1
		}
	}
	if onDelta != nil && (delta.Content != "" || len(delta.ToolCalls) > 0 || len(delta.Thinking) > 0) {
		if err := onDelta(delta); err != nil {
			return err
		}
	}
	return nil
}

// response assembles the final normalized response.
func (a *streamAssembler) response() model.Response {
	calls := a.calls
	if len(calls) == 0 {
		calls = nil
	}
	thinking := a.thinking
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
	}
}
