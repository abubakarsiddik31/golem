package bedrock

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/abubakarsiddik31/golem/model"
)

// streamStatuses maps in-stream exception types to the HTTP statuses
// whose retryability they share, so a throttlingException event and a
// 429 response classify identically through APIError. The status is a
// canonical mapping, not an observed one: exception frames ride a 200
// stream.
var streamStatuses = map[string]int{
	"throttlingexception":         http.StatusTooManyRequests,
	"modeltimeoutexception":       http.StatusRequestTimeout,
	"internalserverexception":     http.StatusInternalServerError,
	"serviceunavailableexception": http.StatusServiceUnavailable,
	"modelstreamerrorexception":   http.StatusInternalServerError,
	"validationexception":         http.StatusBadRequest,
}

// GenerateStream is Generate over the ConverseStream endpoint: the same
// request translation and signing, the same error classification, and
// the same normalized Response — with progress reported as fragments
// arrive. The response body is AWS event-stream framed; readStreamMessage
// decodes it. Reasoning blocks stream as reasoningContent deltas with
// their signatures.
func (c *Client) GenerateStream(ctx context.Context, request model.Request, onDelta func(model.Delta) error) (model.Response, error) {
	httpRequest, err := c.newConverseHTTPRequest(ctx, request, "/converse-stream")
	if err != nil {
		return model.Response{}, err
	}

	httpResponse, err := c.http.Do(httpRequest)
	if err != nil {
		return model.Response{}, &TransportError{Err: err}
	}
	defer httpResponse.Body.Close()

	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		payload, err := io.ReadAll(httpResponse.Body)
		if err != nil {
			return model.Response{}, &TransportError{Err: err}
		}
		return model.Response{}, newAPIError(httpResponse.StatusCode, payload, httpResponse.Header.Get("x-amzn-errortype"))
	}
	return readConverseStream(httpResponse.Body, onDelta)
}

// wire event payloads; union members the adapter does not carry
// (citations, images) stay unknown fields and are skipped.
type streamContentBlockStart struct {
	ContentBlockIndex int `json:"contentBlockIndex"`
	Start             struct {
		ToolUse *struct {
			ToolUseID string `json:"toolUseId"`
			Name      string `json:"name"`
		} `json:"toolUse"`
	} `json:"start"`
}

type streamContentBlockDelta struct {
	ContentBlockIndex int `json:"contentBlockIndex"`
	Delta             struct {
		Text    string `json:"text"`
		ToolUse *struct {
			Input string `json:"input"`
		} `json:"toolUse"`
		// ReasoningContent carries a fragment of one reasoning block: a
		// piece of the visible text, the block's whole signature, or the
		// encrypted payload.
		ReasoningContent *struct {
			Text            string `json:"text"`
			Signature       string `json:"signature"`
			RedactedContent string `json:"redactedContent"`
		} `json:"reasoningContent"`
	} `json:"delta"`
}

type streamMetadata struct {
	Usage struct {
		InputTokens  int `json:"inputTokens"`
		OutputTokens int `json:"outputTokens"`
	} `json:"usage"`
}

// readConverseStream consumes the event-stream body, forwarding
// fragments through onDelta and assembling the Response Generate would
// return: text fragments join into content — separate text blocks
// separated by newlines, matching the non-streaming normalization —,
// toolUse blocks accumulate their JSON arguments, reasoningContent deltas
// assemble thinking blocks with signatures, and the metadata
// event carries usage. A stream that ends without messageStop is a
// truncated response, not a silent partial one.
func readConverseStream(body io.Reader, onDelta func(model.Delta) error) (model.Response, error) {
	report := func(delta model.Delta) error {
		if onDelta == nil {
			return nil
		}
		return onDelta(delta)
	}

	reader := bufio.NewReaderSize(body, 64*1024)

	var content strings.Builder
	var calls []model.ToolCall
	blockCall := map[int]int{}         // content block index → ToolCalls ordinal
	args := map[int]*strings.Builder{} // ToolCalls ordinal → accumulated arguments
	thinkingBlock := map[int]int{}     // content block index → Thinking ordinal
	var thinking []model.ThinkingBlock
	lastTextBlock := -1
	sawMessageStop := false
	var usage model.Usage
	var finish model.FinishReason

	for {
		message, err := readStreamMessage(reader)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return model.Response{}, &DecodeError{Stage: "decode event stream", Err: err}
		}
		if exceptionType, ok := message.Headers[":exception-type"]; ok {
			return model.Response{}, newStreamAPIError(exceptionType, message.Payload)
		}
		switch message.Headers[":event-type"] {
		case "contentBlockStart":
			var event streamContentBlockStart
			if err := json.Unmarshal(message.Payload, &event); err != nil {
				return model.Response{}, &DecodeError{Stage: "decode contentBlockStart", Err: err}
			}
			if event.Start.ToolUse == nil {
				continue
			}
			ordinal := len(calls)
			blockCall[event.ContentBlockIndex] = ordinal
			args[ordinal] = &strings.Builder{}
			calls = append(calls, model.ToolCall{
				ID:   event.Start.ToolUse.ToolUseID,
				Name: event.Start.ToolUse.Name,
			})
			if err := report(model.Delta{ToolCalls: []model.ToolCallDelta{{
				Index: ordinal,
				ID:    event.Start.ToolUse.ToolUseID,
				Name:  event.Start.ToolUse.Name,
			}}}); err != nil {
				return model.Response{}, err
			}
		case "contentBlockDelta":
			var event streamContentBlockDelta
			if err := json.Unmarshal(message.Payload, &event); err != nil {
				return model.Response{}, &DecodeError{Stage: "decode contentBlockDelta", Err: err}
			}
			switch {
			case event.Delta.Text != "":
				if lastTextBlock != -1 && lastTextBlock != event.ContentBlockIndex {
					content.WriteString("\n")
				}
				lastTextBlock = event.ContentBlockIndex
				content.WriteString(event.Delta.Text)
				if err := report(model.Delta{Content: event.Delta.Text}); err != nil {
					return model.Response{}, err
				}
			case event.Delta.ReasoningContent != nil:
				reasoning := event.Delta.ReasoningContent
				ordinal, ok := thinkingBlock[event.ContentBlockIndex]
				if !ok {
					ordinal = len(thinking)
					thinkingBlock[event.ContentBlockIndex] = ordinal
					thinking = append(thinking, model.ThinkingText(""))
				}
				switch {
				case reasoning.RedactedContent != "":
					thinking[ordinal] = model.ThinkingRedacted(reasoning.RedactedContent)
				case reasoning.Signature != "":
					thinking[ordinal].Signature = reasoning.Signature
				case reasoning.Text != "":
					thinking[ordinal].Text += reasoning.Text
				}
				if reasoning.Text != "" || reasoning.Signature != "" {
					if err := report(model.Delta{Thinking: []model.ThinkingDelta{{
						Index:     ordinal,
						Text:      reasoning.Text,
						Signature: reasoning.Signature,
					}}}); err != nil {
						return model.Response{}, err
					}
				}
			case event.Delta.ToolUse != nil:
				ordinal, ok := blockCall[event.ContentBlockIndex]
				if !ok {
					return model.Response{}, &DecodeError{
						Stage: "decode contentBlockDelta",
						Err:   fmt.Errorf("toolUse delta for block %d without a contentBlockStart", event.ContentBlockIndex),
					}
				}
				args[ordinal].WriteString(event.Delta.ToolUse.Input)
				if err := report(model.Delta{ToolCalls: []model.ToolCallDelta{{
					Index:        ordinal,
					ArgsFragment: event.Delta.ToolUse.Input,
				}}}); err != nil {
					return model.Response{}, err
				}
			}
		case "messageStop":
			sawMessageStop = true
			var event streamMessageStop
			if err := json.Unmarshal(message.Payload, &event); err != nil {
				return model.Response{}, &DecodeError{Stage: "decode messageStop", Err: err}
			}
			finish = finishReason(event.StopReason)
		case "metadata":
			var event streamMetadata
			if err := json.Unmarshal(message.Payload, &event); err != nil {
				return model.Response{}, &DecodeError{Stage: "decode metadata", Err: err}
			}
			usage = model.Usage{
				InputTokens:  event.Usage.InputTokens,
				OutputTokens: event.Usage.OutputTokens,
			}
		}
	}
	if !sawMessageStop {
		return model.Response{}, &DecodeError{
			Stage: "decode event stream",
			Err:   errors.New("stream ended without a messageStop event"),
		}
	}

	for ordinal := range calls {
		calls[ordinal].Args = normalizeInput(json.RawMessage(args[ordinal].String()))
	}
	if len(thinking) == 0 {
		thinking = nil
	}
	return model.Response{
		Message: model.Message{
			Role:      model.RoleAssistant,
			Content:   content.String(),
			ToolCalls: calls,
			Thinking:  thinking,
		},
		Usage:        usage,
		FinishReason: finish,
	}, nil
}

// streamMessageStop is the terminal event of a Converse stream; its
// stopReason uses the same vocabulary as the non-streaming response.
type streamMessageStop struct {
	StopReason string `json:"stopReason"`
}

// newStreamAPIError classifies an in-stream exception frame. Payloads
// carry {message}, the same body shape HTTP errors use.
func newStreamAPIError(exceptionType string, payload []byte) *APIError {
	status, ok := streamStatuses[strings.ToLower(exceptionType)]
	if !ok {
		status = http.StatusInternalServerError
	}
	return newAPIError(status, payload, exceptionType)
}
