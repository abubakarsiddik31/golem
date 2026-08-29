package bedrock_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/bedrock"
)

// frame builds one AWS event-stream frame carrying string headers and a
// JSON payload — the wire ConverseStream responds with.
func frame(headers map[string]string, payload string) []byte {
	var block []byte
	add := func(name, value string) {
		block = append(block, byte(len(name)))
		block = append(block, name...)
		block = append(block, 7) // string value
		var length [2]byte
		binary.BigEndian.PutUint16(length[:], uint16(len(value)))
		block = append(block, length[:]...)
		block = append(block, value...)
	}
	add(":message-type", "event")
	for name, value := range headers {
		add(name, value)
	}
	prelude := make([]byte, 8)
	binary.BigEndian.PutUint32(prelude[0:4], uint32(16+len(block)+len(payload)))
	binary.BigEndian.PutUint32(prelude[4:8], uint32(len(block)))
	hasher := crc32.NewIEEE()
	hasher.Write(prelude)
	hasher.Write(block)
	hasher.Write([]byte(payload))

	out := prelude
	var preludeCRC [4]byte
	binary.BigEndian.PutUint32(preludeCRC[:], crc32.ChecksumIEEE(prelude))
	out = append(out, preludeCRC[:]...)
	out = append(out, block...)
	out = append(out, payload...)
	var messageCRC [4]byte
	binary.BigEndian.PutUint32(messageCRC[:], hasher.Sum32())
	return append(out, messageCRC[:]...)
}

func eventFrame(eventType, payload string) []byte {
	return frame(map[string]string{":event-type": eventType}, payload)
}

// streamServer replies to converse-stream with a 200 event-stream body
// built from scripted frames.
func streamServer(t *testing.T, status int, frames ...[]byte) *recordedServer {
	t.Helper()
	body := make([]byte, 0, 1024)
	for _, one := range frames {
		body = append(body, one...)
	}
	server := newRecordedServer(status, string(body), map[string]string{
		"Content-Type": "application/vnd.amazon.eventstream",
	})
	return server
}

func TestGenerateStreamTextFragments(t *testing.T) {
	server := streamServer(t, http.StatusOK,
		eventFrame("messageStart", `{"role":"assistant"}`),
		eventFrame("contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"Hel"}}`),
		eventFrame("contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"lo"}}`),
		eventFrame("contentBlockStop", `{"contentBlockIndex":0}`),
		eventFrame("messageStop", `{"stopReason":"end_turn"}`),
		eventFrame("metadata", `{"usage":{"inputTokens":4,"outputTokens":6}}`),
	)
	defer server.server.Close()
	client := newClient(t, server.server.URL)

	var deltas []model.Delta
	response, err := client.GenerateStream(context.Background(), model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	}, func(delta model.Delta) error {
		deltas = append(deltas, delta)
		return nil
	})
	if err != nil {
		t.Fatalf("GenerateStream error = %v", err)
	}
	if len(deltas) != 2 || deltas[0].Content != "Hel" || deltas[1].Content != "lo" {
		t.Errorf("deltas = %+v, want two text fragments", deltas)
	}
	if response.Message.Content != "Hello" || response.Message.Role != model.RoleAssistant {
		t.Errorf("message = %+v", response.Message)
	}
	if response.Usage.InputTokens != 4 || response.Usage.OutputTokens != 6 {
		t.Errorf("usage = %+v", response.Usage)
	}

	request := server.last(t)
	if request.path != "/model/anthropic.claude-sonnet-4-5-20250929-v1:0/converse-stream" {
		t.Errorf("path = %q, want the converse-stream endpoint", request.path)
	}
	if !strings.Contains(request.authorizer, "AWS4-HMAC-SHA256") {
		t.Error("request is not SigV4 signed")
	}
}

func TestGenerateStreamToolUse(t *testing.T) {
	server := streamServer(t, http.StatusOK,
		eventFrame("messageStart", `{"role":"assistant"}`),
		eventFrame("contentBlockStart", `{"contentBlockIndex":1,"start":{"toolUse":{"toolUseId":"call-1","name":"get_weather"}}}`),
		eventFrame("contentBlockDelta", `{"contentBlockIndex":1,"delta":{"toolUse":{"input":"{\"city\":"}}}`),
		eventFrame("contentBlockDelta", `{"contentBlockIndex":1,"delta":{"toolUse":{"input":"\"Paris\"}"}}}`),
		eventFrame("contentBlockStop", `{"contentBlockIndex":1}`),
		eventFrame("messageStop", `{"stopReason":"tool_use"}`),
		eventFrame("metadata", `{"usage":{"inputTokens":9,"outputTokens":12}}`),
	)
	defer server.server.Close()
	client := newClient(t, server.server.URL)

	var deltas []model.Delta
	response, err := client.GenerateStream(context.Background(), model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "weather in paris?"}},
	}, func(delta model.Delta) error {
		deltas = append(deltas, delta)
		return nil
	})
	if err != nil {
		t.Fatalf("GenerateStream error = %v", err)
	}
	wantCalls := []model.ToolCall{{
		ID:   "call-1",
		Name: "get_weather",
		Args: json.RawMessage(`{"city":"Paris"}`),
	}}
	if !reflect.DeepEqual(response.Message.ToolCalls, wantCalls) {
		t.Errorf("tool calls = %+v, want %+v", response.Message.ToolCalls, wantCalls)
	}
	if len(deltas) != 3 {
		t.Fatalf("deltas = %+v, want identification plus two argument fragments", deltas)
	}
	first := deltas[0].ToolCalls[0]
	if first.Index != 0 || first.ID != "call-1" || first.Name != "get_weather" || first.ArgsFragment != "" {
		t.Errorf("identification delta = %+v", first)
	}
	if deltas[1].ToolCalls[0].ArgsFragment != `{"city":` || deltas[2].ToolCalls[0].ArgsFragment != `"Paris"}` {
		t.Errorf("argument deltas = %+v %+v", deltas[1].ToolCalls[0], deltas[2].ToolCalls[0])
	}
}

func TestGenerateStreamSeparateTextBlocksJoin(t *testing.T) {
	server := streamServer(t, http.StatusOK,
		eventFrame("messageStart", `{"role":"assistant"}`),
		eventFrame("contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"first"}}`),
		eventFrame("contentBlockStop", `{"contentBlockIndex":0}`),
		eventFrame("contentBlockDelta", `{"contentBlockIndex":2,"delta":{"text":"second"}}`),
		eventFrame("contentBlockStop", `{"contentBlockIndex":2}`),
		eventFrame("messageStop", `{"stopReason":"end_turn"}`),
		eventFrame("metadata", `{"usage":{"inputTokens":1,"outputTokens":2}}`),
	)
	defer server.server.Close()
	client := newClient(t, server.server.URL)

	response, err := client.GenerateStream(context.Background(), model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	}, func(model.Delta) error { return nil })
	if err != nil {
		t.Fatalf("GenerateStream error = %v", err)
	}
	if response.Message.Content != "first\nsecond" {
		t.Errorf("content = %q, want text blocks joined by a newline", response.Message.Content)
	}
}

func TestGenerateStreamInStreamException(t *testing.T) {
	exception := frame(map[string]string{
		":message-type":   "exception",
		":exception-type": "throttlingException",
	}, `{"message":"Too many requests"}`)
	server := streamServer(t, http.StatusOK,
		eventFrame("messageStart", `{"role":"assistant"}`),
		exception,
	)
	defer server.server.Close()
	client := newClient(t, server.server.URL)

	_, err := client.GenerateStream(context.Background(), model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	}, func(model.Delta) error { return nil })
	var apiErr *bedrock.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("GenerateStream error = %v, want *bedrock.APIError in the chain", err)
	}
	if apiErr.Code != "throttlingException" || !strings.Contains(apiErr.Message, "Too many requests") {
		t.Errorf("APIError = %q %q", apiErr.Code, apiErr.Message)
	}
	if !apiErr.Retryable() {
		t.Error("a throttling exception mid-stream must classify as retryable")
	}
}

func TestGenerateStreamHTTPError(t *testing.T) {
	server := newRecordedServer(http.StatusForbidden, `{"message":"denied"}`, map[string]string{
		"x-amzn-errortype": "AccessDeniedException",
	})
	defer server.server.Close()
	client := newClient(t, server.server.URL)

	_, err := client.GenerateStream(context.Background(), model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	}, func(model.Delta) error { return nil })
	var apiErr *bedrock.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "AccessDeniedException" {
		t.Fatalf("GenerateStream error = %v, want an APIError carrying the exception type", err)
	}
}

func TestGenerateStreamRejectsTruncatedStream(t *testing.T) {
	server := streamServer(t, http.StatusOK,
		eventFrame("messageStart", `{"role":"assistant"}`),
		eventFrame("contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"partial"}}`),
		// No messageStop: the stream ends early.
	)
	defer server.server.Close()
	client := newClient(t, server.server.URL)

	_, err := client.GenerateStream(context.Background(), model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	}, func(model.Delta) error { return nil })
	var decodeErr *bedrock.DecodeError
	if !errors.As(err, &decodeErr) {
		t.Fatalf("GenerateStream error = %v, want *bedrock.DecodeError", err)
	}
	if !strings.Contains(decodeErr.Error(), "messageStop") {
		t.Errorf("DecodeError = %v, want the missing messageStop named", decodeErr)
	}
}

func TestGenerateStreamCorruptFrame(t *testing.T) {
	valid := eventFrame("messageStart", `{"role":"assistant"}`)
	corrupt := eventFrame("contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"x"}}`)
	corrupt[len(corrupt)-5] ^= 0xff
	server := streamServer(t, http.StatusOK, valid, corrupt)
	defer server.server.Close()
	client := newClient(t, server.server.URL)

	_, err := client.GenerateStream(context.Background(), model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	}, func(model.Delta) error { return nil })
	var decodeErr *bedrock.DecodeError
	if !errors.As(err, &decodeErr) {
		t.Fatalf("GenerateStream error = %v, want *bedrock.DecodeError for a CRC failure", err)
	}
}

func TestGenerateStreamOnDeltaStopsStream(t *testing.T) {
	server := streamServer(t, http.StatusOK,
		eventFrame("messageStart", `{"role":"assistant"}`),
		eventFrame("contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"first"}}`),
		eventFrame("contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"never"}}`),
		eventFrame("messageStop", `{"stopReason":"end_turn"}`),
	)
	defer server.server.Close()
	client := newClient(t, server.server.URL)

	stopped := errors.New("observer decided to stop")
	_, err := client.GenerateStream(context.Background(), model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	}, func(delta model.Delta) error {
		return stopped
	})
	if !errors.Is(err, stopped) {
		t.Errorf("GenerateStream error = %v, want the observer error returned as-is", err)
	}
}

func TestGenerateStreamPropagatesCancellation(t *testing.T) {
	server := streamServer(t, http.StatusOK,
		eventFrame("messageStart", `{"role":"assistant"}`),
		eventFrame("messageStop", `{"stopReason":"end_turn"}`),
	)
	defer server.server.Close()
	client := newClient(t, server.server.URL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.GenerateStream(ctx, model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	}, func(model.Delta) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Errorf("GenerateStream error = %v, want context.Canceled in the chain", err)
	}
}

func TestGenerateStreamImplementsStreamingModel(t *testing.T) {
	var _ model.StreamingModel = newClient(t, "http://localhost:1")
}

func TestGenerateStreamReasoningFragments(t *testing.T) {
	server := streamServer(t, http.StatusOK,
		eventFrame("messageStart", `{"role":"assistant"}`),
		eventFrame("contentBlockDelta", `{"contentBlockIndex":0,"delta":{"reasoningContent":{"text":"step"}}}`),
		eventFrame("contentBlockDelta", `{"contentBlockIndex":0,"delta":{"reasoningContent":{"text":" by step"}}}`),
		eventFrame("contentBlockDelta", `{"contentBlockIndex":0,"delta":{"reasoningContent":{"signature":"sig-1"}}}`),
		eventFrame("contentBlockDelta", `{"contentBlockIndex":1,"delta":{"reasoningContent":{"redactedContent":"enc"}}}`),
		eventFrame("contentBlockDelta", `{"contentBlockIndex":2,"delta":{"text":"The answer is 4."}}`),
		eventFrame("contentBlockStop", `{"contentBlockIndex":2}`),
		eventFrame("messageStop", `{"stopReason":"end_turn"}`),
		eventFrame("metadata", `{"usage":{"inputTokens":9,"outputTokens":12}}`),
	)
	defer server.server.Close()
	client := newClient(t, server.server.URL)

	var deltas []model.Delta
	response, err := client.GenerateStream(context.Background(), model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	}, func(delta model.Delta) error {
		deltas = append(deltas, delta)
		return nil
	})
	if err != nil {
		t.Fatalf("GenerateStream error = %v", err)
	}

	// Reasoning deltas arrive before the text fragment, signatures included;
	// redacted payloads ride the assembled response only, as on the wire.
	thinkingDeltas := 0
	for _, delta := range deltas {
		thinkingDeltas += len(delta.Thinking)
	}
	if thinkingDeltas != 3 {
		t.Errorf("thinking deltas = %d, want 3 (two text fragments, one signature)", thinkingDeltas)
	}
	if deltas[0].Thinking[0].Text != "step" || deltas[1].Thinking[0].Text != " by step" ||
		deltas[2].Thinking[0].Signature != "sig-1" || deltas[0].Thinking[0].Index != 0 {
		t.Errorf("thinking deltas = %+v", deltas[:3])
	}
	thinking := response.Message.Thinking
	if len(thinking) != 2 || thinking[0].Text != "step by step" || thinking[0].Signature != "sig-1" ||
		thinking[1].Redacted != "enc" {
		t.Errorf("assembled thinking = %+v, want signed block plus redacted block", thinking)
	}
	if response.Message.Content != "The answer is 4." {
		t.Errorf("content = %q", response.Message.Content)
	}
	if response.Usage.InputTokens != 9 || response.Usage.OutputTokens != 12 {
		t.Errorf("usage = %+v", response.Usage)
	}
}
