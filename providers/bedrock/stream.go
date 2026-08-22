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
// decodes it.
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
// (reasoning, citations, images) stay unknown fields and are skipped.
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
// toolUse blocks accumulate their JSON arguments, and the metadata
// event carries usage. A stream that ends without messageStop is a
// truncated response, not a silent partial one.
func readConverseStream(body io.Reader, onDelta func(model.Delta) error) (model.Response, error) {
	reader := bufio.NewReaderSize(body, 64*1024)

	var content strings.Builder
	var calls []model.ToolCall
	blockCall := map[int]int{}         // content block index → ToolCalls ordinal
	args := map[int]*strings.Builder{} // ToolCalls ordinal → accumulated arguments
	lastTextBlock := -1
	sawMessageStop := false
	var usage model.Usage

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
			if err := onDelta(model.Delta{ToolCalls: []model.ToolCallDelta{{
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
				if err := onDelta(model.Delta{Content: event.Delta.Text}); err != nil {
					return model.Response{}, err
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
				if err := onDelta(model.Delta{ToolCalls: []model.ToolCallDelta{{
					Index:        ordinal,
					ArgsFragment: event.Delta.ToolUse.Input,
				}}}); err != nil {
					return model.Response{}, err
				}
			}
		case "messageStop":
			sawMessageStop = true
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
	return model.Response{
		Message: model.Message{
			Role:      model.RoleAssistant,
			Content:   content.String(),
			ToolCalls: calls,
		},
		Usage: usage,
	}, nil
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
