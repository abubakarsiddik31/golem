package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// sentFor returns the params of every sent message with method, in order.
func sentFor(t *testing.T, transport *funcTransport, method string) []json.RawMessage {
	t.Helper()
	var params []json.RawMessage
	for i := range transport.sent {
		message := decodeSent(t, transport, i)
		if message.Method == method {
			params = append(params, message.Params)
		}
	}
	return params
}

// capabilitiesOf decodes a request's declared client capabilities.
func capabilitiesOf(t *testing.T, params json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	raw, ok := metaOf(t, params)["io.modelcontextprotocol/clientCapabilities"]
	if !ok {
		t.Fatalf("params %s declare no client capabilities", params)
	}
	var capabilities map[string]json.RawMessage
	if err := json.Unmarshal(raw, &capabilities); err != nil {
		t.Fatalf("client capabilities %s: %v", raw, err)
	}
	return capabilities
}

// elicitationReply builds a modern tools/call result asking one form
// elicitation, with the given state (omitted when empty).
func elicitationReply(t *testing.T, id json.RawMessage, request, state string) []byte {
	t.Helper()
	result := `{"resultType":"input_required","inputRequests":{"ask":` + request + `}`
	if state != "" {
		result += `,"requestState":` + state
	}
	result += `}`
	return serverReply(id, result)
}

const formRequest = `{"method":"elicitation/create","params":{"message":"Which name?","mode":"form","requestedSchema":{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}}}`

func TestElicitationFormRoundTrip(t *testing.T) {
	t.Parallel()

	var seen []Elicitation
	calls := 0
	transport := &funcTransport{handle: modernHandler(t, func(method string, id json.RawMessage, _ json.RawMessage) [][]byte {
		if method != "tools/call" {
			return nil
		}
		calls++
		if calls == 1 {
			return [][]byte{elicitationReply(t, id, formRequest, `"opaque-state"`)}
		}
		return [][]byte{serverReply(id, `{"resultType":"complete","content":[{"type":"text","text":"hello Ada"}]}`)}
	})}
	client := NewClient(transport)
	client.OnElicitation(func(_ context.Context, request Elicitation) (ElicitationResult, error) {
		seen = append(seen, request)
		return ElicitationResult{Action: ElicitationAccept, Content: json.RawMessage(`{"name":"Ada"}`)}, nil
	})
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}

	result, err := client.CallTool(context.Background(), "echo", json.RawMessage(`{"msg":"hi"}`))
	if err != nil {
		t.Fatalf("CallTool error = %v", err)
	}
	if result.Text() != "hello Ada" {
		t.Fatalf("CallTool result = %q, want the retried result", result.Text())
	}
	if len(seen) != 1 {
		t.Fatalf("handler ran %d times, want 1", len(seen))
	}
	request := seen[0]
	if request.Message != "Which name?" || request.Mode != ElicitationModeForm {
		t.Errorf("Elicitation = %+v, want the form request", request)
	}
	if canonicalJSON(request.RequestedSchema) != canonicalJSON(json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`)) {
		t.Errorf("RequestedSchema = %s, want the server's verbatim", request.RequestedSchema)
	}

	params := sentFor(t, transport, "tools/call")
	if len(params) != 2 {
		t.Fatalf("tools/call sent %d times, want an initial request and one retry", len(params))
	}
	var retry struct {
		Name           string                     `json:"name"`
		Arguments      json.RawMessage            `json:"arguments"`
		InputResponses map[string]json.RawMessage `json:"inputResponses"`
		RequestState   string                     `json:"requestState"`
	}
	if err := json.Unmarshal(params[1], &retry); err != nil {
		t.Fatalf("retry params %s: %v", params[1], err)
	}
	if retry.Name != "echo" || canonicalJSON(retry.Arguments) != canonicalJSON(json.RawMessage(`{"msg":"hi"}`)) {
		t.Errorf("retry params = %+v, want the original request preserved", retry)
	}
	if retry.RequestState != "opaque-state" {
		t.Errorf("requestState = %q, want the server's verbatim", retry.RequestState)
	}
	if got := canonicalJSON(retry.InputResponses["ask"]); got != canonicalJSON(json.RawMessage(`{"action":"accept","content":{"name":"Ada"}}`)) {
		t.Errorf("inputResponses[ask] = %s, want the accepted form", got)
	}
	// The retry is an independent request, so its JSON-RPC id differs.
	var ids []json.RawMessage
	for i := range transport.sent {
		message := decodeSent(t, transport, i)
		if message.Method == "tools/call" {
			ids = append(ids, message.ID)
		}
	}
	if len(ids) != 2 {
		t.Fatalf("tools/call ids = %v, want two", ids)
	}
	if sameID(ids[0], ids[1]) {
		t.Errorf("initial and retry share id %s", ids[0])
	}
}

func TestElicitationCapabilityDeclaredOnlyWithHandler(t *testing.T) {
	t.Parallel()

	transport := &funcTransport{handle: modernHandler(t, func(method string, id json.RawMessage, _ json.RawMessage) [][]byte {
		if method != "tools/list" {
			return nil
		}
		return [][]byte{serverReply(id, `{"resultType":"complete","tools":[]}`)}
	})}
	client := NewClient(transport)
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	if _, err := client.ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools error = %v", err)
	}
	if _, ok := capabilitiesOf(t, sentParams(t, transport, "tools/list"))["elicitation"]; ok {
		t.Fatal("elicitation declared without a handler")
	}

	client.OnElicitation(func(context.Context, Elicitation) (ElicitationResult, error) {
		return ElicitationResult{Action: ElicitationDecline}, nil
	})
	if _, err := client.ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools error = %v", err)
	}
	raw, ok := capabilitiesOf(t, sentParams(t, transport, "tools/list"))["elicitation"]
	if !ok {
		t.Fatal("elicitation not declared with a handler installed")
	}
	var modes map[string]json.RawMessage
	if err := json.Unmarshal(raw, &modes); err != nil {
		t.Fatalf("elicitation capability %s: %v", raw, err)
	}
	for _, mode := range []string{"form", "url"} {
		if _, ok := modes[mode]; !ok {
			t.Errorf("elicitation capability %s is missing %s", raw, mode)
		}
	}

	// Removing the handler stops the declaration.
	client.OnElicitation(nil)
	if _, err := client.ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools error = %v", err)
	}
	if _, ok := capabilitiesOf(t, sentParams(t, transport, "tools/list"))["elicitation"]; ok {
		t.Fatal("elicitation still declared after the handler was removed")
	}
}

func TestElicitationWithoutHandlerIsReported(t *testing.T) {
	t.Parallel()

	transport := &funcTransport{handle: modernHandler(t, func(method string, id json.RawMessage, _ json.RawMessage) [][]byte {
		if method != "tools/call" {
			return nil
		}
		return [][]byte{elicitationReply(t, id, formRequest, "")}
	})}
	client := NewClient(transport)
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	_, err := client.CallTool(context.Background(), "echo", nil)
	var input *InputRequiredError
	if !errors.As(err, &input) {
		t.Fatalf("CallTool error = %v, want *InputRequiredError", err)
	}
	if input.Method != elicitationCreate {
		t.Errorf("InputRequiredError.Method = %q, want %q", input.Method, elicitationCreate)
	}
	if len(input.Result) == 0 {
		t.Error("InputRequiredError.Result is empty, want the server's result")
	}
}

func TestElicitationUnsupportedRequestKindIsReported(t *testing.T) {
	t.Parallel()

	transport := &funcTransport{handle: modernHandler(t, func(method string, id json.RawMessage, _ json.RawMessage) [][]byte {
		if method != "tools/call" {
			return nil
		}
		return [][]byte{serverReply(id, `{"resultType":"input_required","inputRequests":{"ask":{"method":"sampling/createMessage","params":{}}}}`)}
	})}
	client := NewClient(transport)
	client.OnElicitation(func(context.Context, Elicitation) (ElicitationResult, error) {
		t.Error("handler ran for an unsupported request kind")
		return ElicitationResult{Action: ElicitationDecline}, nil
	})
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	_, err := client.CallTool(context.Background(), "echo", nil)
	var input *InputRequiredError
	if !errors.As(err, &input) {
		t.Fatalf("CallTool error = %v, want *InputRequiredError", err)
	}
	if input.Method != "sampling/createMessage" {
		t.Errorf("InputRequiredError.Method = %q, want sampling/createMessage", input.Method)
	}
	if !strings.Contains(string(input.Result), "inputRequests") {
		t.Errorf("InputRequiredError.Result = %s, want the whole input_required result", input.Result)
	}
}

func TestElicitationDeclineOmitsContent(t *testing.T) {
	t.Parallel()

	calls := 0
	transport := &funcTransport{handle: modernHandler(t, func(method string, id json.RawMessage, _ json.RawMessage) [][]byte {
		if method != "tools/call" {
			return nil
		}
		calls++
		if calls == 1 {
			return [][]byte{elicitationReply(t, id, formRequest, "")}
		}
		return [][]byte{serverReply(id, `{"resultType":"complete","content":[{"type":"text","text":"declined"}]}`)}
	})}
	client := NewClient(transport)
	client.OnElicitation(func(context.Context, Elicitation) (ElicitationResult, error) {
		// Content is ignored unless the action accepts a form.
		return ElicitationResult{Action: ElicitationDecline, Content: json.RawMessage(`{"name":"Ada"}`)}, nil
	})
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	if _, err := client.CallTool(context.Background(), "echo", nil); err != nil {
		t.Fatalf("CallTool error = %v", err)
	}

	params := sentFor(t, transport, "tools/call")
	var retry struct {
		InputResponses map[string]json.RawMessage `json:"inputResponses"`
	}
	if err := json.Unmarshal(params[1], &retry); err != nil {
		t.Fatalf("retry params %s: %v", params[1], err)
	}
	if got := canonicalJSON(retry.InputResponses["ask"]); got != canonicalJSON(json.RawMessage(`{"action":"decline"}`)) {
		t.Errorf("inputResponses[ask] = %s, want a bare decline", got)
	}
}

func TestElicitationURLModeSurfacesTheURL(t *testing.T) {
	t.Parallel()

	var seen Elicitation
	calls := 0
	transport := &funcTransport{handle: modernHandler(t, func(method string, id json.RawMessage, _ json.RawMessage) [][]byte {
		if method != "tools/call" {
			return nil
		}
		calls++
		if calls == 1 {
			return [][]byte{elicitationReply(t, id,
				`{"method":"elicitation/create","params":{"message":"Authorize the connection","mode":"url","url":"https://example.com/connect"}}`, "")}
		}
		return [][]byte{serverReply(id, `{"resultType":"complete","content":[{"type":"text","text":"connected"}]}`)}
	})}
	client := NewClient(transport)
	client.OnElicitation(func(_ context.Context, request Elicitation) (ElicitationResult, error) {
		seen = request
		return ElicitationResult{Action: ElicitationAccept}, nil
	})
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	if _, err := client.CallTool(context.Background(), "echo", nil); err != nil {
		t.Fatalf("CallTool error = %v", err)
	}
	if seen.Mode != ElicitationModeURL || seen.URL != "https://example.com/connect" || seen.Message != "Authorize the connection" {
		t.Errorf("Elicitation = %+v, want the url request", seen)
	}

	params := sentFor(t, transport, "tools/call")
	var retry struct {
		InputResponses map[string]json.RawMessage `json:"inputResponses"`
	}
	if err := json.Unmarshal(params[1], &retry); err != nil {
		t.Fatalf("retry params %s: %v", params[1], err)
	}
	// Out-of-band interactions never carry form content.
	if got := canonicalJSON(retry.InputResponses["ask"]); got != canonicalJSON(json.RawMessage(`{"action":"accept"}`)) {
		t.Errorf("inputResponses[ask] = %s, want a content-free accept", got)
	}
}

func TestElicitationDefaultsToFormMode(t *testing.T) {
	t.Parallel()

	var seen Elicitation
	calls := 0
	transport := &funcTransport{handle: modernHandler(t, func(method string, id json.RawMessage, _ json.RawMessage) [][]byte {
		if method != "tools/call" {
			return nil
		}
		calls++
		if calls == 1 {
			// The mode field is omitted, as the protocol permits.
			return [][]byte{elicitationReply(t, id, `{"method":"elicitation/create","params":{"message":"Name?","requestedSchema":{"type":"object","properties":{}}}}`, "")}
		}
		return [][]byte{serverReply(id, `{"resultType":"complete","content":[]}`)}
	})}
	client := NewClient(transport)
	client.OnElicitation(func(_ context.Context, request Elicitation) (ElicitationResult, error) {
		seen = request
		return ElicitationResult{Action: ElicitationCancel}, nil
	})
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	if _, err := client.CallTool(context.Background(), "echo", nil); err != nil {
		t.Fatalf("CallTool error = %v", err)
	}
	if seen.Mode != ElicitationModeForm {
		t.Fatalf("Elicitation.Mode = %q, want form", seen.Mode)
	}
}

func TestElicitationAnswersEveryRequestInKeyOrder(t *testing.T) {
	t.Parallel()

	var answered []string
	calls := 0
	transport := &funcTransport{handle: modernHandler(t, func(method string, id json.RawMessage, _ json.RawMessage) [][]byte {
		if method != "tools/call" {
			return nil
		}
		calls++
		if calls == 1 {
			return [][]byte{serverReply(id, `{"resultType":"input_required","inputRequests":{
				"second":{"method":"elicitation/create","params":{"message":"Second","mode":"form","requestedSchema":{"type":"object","properties":{}}}},
				"first":{"method":"elicitation/create","params":{"message":"First","mode":"form","requestedSchema":{"type":"object","properties":{}}}}
			}}`)}
		}
		return [][]byte{serverReply(id, `{"resultType":"complete","content":[]}`)}
	})}
	client := NewClient(transport)
	client.OnElicitation(func(_ context.Context, request Elicitation) (ElicitationResult, error) {
		answered = append(answered, request.Message)
		return ElicitationResult{Action: ElicitationAccept, Content: json.RawMessage(`{}`)}, nil
	})
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	if _, err := client.CallTool(context.Background(), "echo", nil); err != nil {
		t.Fatalf("CallTool error = %v", err)
	}
	if len(answered) != 2 || answered[0] != "First" || answered[1] != "Second" {
		t.Errorf("answered = %v, want first then second", answered)
	}

	params := sentFor(t, transport, "tools/call")
	var retry struct {
		InputResponses map[string]json.RawMessage `json:"inputResponses"`
	}
	if err := json.Unmarshal(params[1], &retry); err != nil {
		t.Fatalf("retry params %s: %v", params[1], err)
	}
	for _, key := range []string{"first", "second"} {
		if _, ok := retry.InputResponses[key]; !ok {
			t.Errorf("inputResponses is missing %q: %v", key, retry.InputResponses)
		}
	}
}

func TestElicitationHandlerErrorFailsTheCall(t *testing.T) {
	t.Parallel()

	transport := &funcTransport{handle: modernHandler(t, func(method string, id json.RawMessage, _ json.RawMessage) [][]byte {
		if method != "tools/call" {
			return nil
		}
		return [][]byte{elicitationReply(t, id, formRequest, "")}
	})}
	client := NewClient(transport)
	client.OnElicitation(func(context.Context, Elicitation) (ElicitationResult, error) {
		return ElicitationResult{}, errors.New("user unavailable")
	})
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	_, err := client.CallTool(context.Background(), "echo", nil)
	if err == nil || !strings.Contains(err.Error(), "user unavailable") {
		t.Fatalf("CallTool error = %v, want the handler's error", err)
	}
}

func TestElicitationUnknownActionFailsTheCall(t *testing.T) {
	t.Parallel()

	transport := &funcTransport{handle: modernHandler(t, func(method string, id json.RawMessage, _ json.RawMessage) [][]byte {
		if method != "tools/call" {
			return nil
		}
		return [][]byte{elicitationReply(t, id, formRequest, "")}
	})}
	client := NewClient(transport)
	client.OnElicitation(func(context.Context, Elicitation) (ElicitationResult, error) {
		return ElicitationResult{Action: "approve"}, nil
	})
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	_, err := client.CallTool(context.Background(), "echo", nil)
	if err == nil || !strings.Contains(err.Error(), `unknown action "approve"`) {
		t.Fatalf("CallTool error = %v, want an unknown-action failure", err)
	}
}

func TestElicitationRoundLimit(t *testing.T) {
	t.Parallel()

	transport := &funcTransport{handle: modernHandler(t, func(method string, id json.RawMessage, _ json.RawMessage) [][]byte {
		if method != "tools/call" {
			return nil
		}
		// A server that never has enough information.
		return [][]byte{elicitationReply(t, id, formRequest, `"state"`)}
	})}
	client := NewClient(transport)
	client.OnElicitation(func(context.Context, Elicitation) (ElicitationResult, error) {
		return ElicitationResult{Action: ElicitationAccept, Content: json.RawMessage(`{"name":"Ada"}`)}, nil
	})
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	_, err := client.CallTool(context.Background(), "echo", nil)
	if err == nil || !strings.Contains(err.Error(), "still needs input after 8 rounds") {
		t.Fatalf("CallTool error = %v, want the round limit", err)
	}
	if got := len(sentFor(t, transport, "tools/call")); got != maxInputRounds+1 {
		t.Errorf("tools/call sent %d times, want %d", got, maxInputRounds+1)
	}
}

func TestInputRequiredWithStateOnlyRetries(t *testing.T) {
	t.Parallel()

	calls := 0
	transport := &funcTransport{handle: modernHandler(t, func(method string, id json.RawMessage, _ json.RawMessage) [][]byte {
		if method != "tools/call" {
			return nil
		}
		calls++
		if calls == 1 {
			return [][]byte{serverReply(id, `{"resultType":"input_required","requestState":"only-state"}`)}
		}
		return [][]byte{serverReply(id, `{"resultType":"complete","content":[{"type":"text","text":"done"}]}`)}
	})}
	client := NewClient(transport)
	client.OnElicitation(func(context.Context, Elicitation) (ElicitationResult, error) {
		t.Error("handler ran for a state-only retry")
		return ElicitationResult{Action: ElicitationDecline}, nil
	})
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	result, err := client.CallTool(context.Background(), "echo", nil)
	if err != nil {
		t.Fatalf("CallTool error = %v", err)
	}
	if result.Text() != "done" {
		t.Errorf("CallTool result = %q, want done", result.Text())
	}
	params := sentFor(t, transport, "tools/call")
	var retry struct {
		InputResponses map[string]json.RawMessage `json:"inputResponses"`
		RequestState   string                     `json:"requestState"`
	}
	if err := json.Unmarshal(params[1], &retry); err != nil {
		t.Fatalf("retry params %s: %v", params[1], err)
	}
	if retry.RequestState != "only-state" {
		t.Errorf("requestState = %q, want only-state", retry.RequestState)
	}
	if retry.InputResponses != nil {
		t.Errorf("inputResponses = %v, want none", retry.InputResponses)
	}
}

func TestInputRequiredWithNothingToAnswerFails(t *testing.T) {
	t.Parallel()

	transport := &funcTransport{handle: modernHandler(t, func(method string, id json.RawMessage, _ json.RawMessage) [][]byte {
		if method != "tools/call" {
			return nil
		}
		return [][]byte{serverReply(id, `{"resultType":"input_required"}`)}
	})}
	client := NewClient(transport)
	client.OnElicitation(func(context.Context, Elicitation) (ElicitationResult, error) {
		t.Error("handler ran for an empty input request")
		return ElicitationResult{Action: ElicitationDecline}, nil
	})
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	_, err := client.CallTool(context.Background(), "echo", nil)
	var input *InputRequiredError
	if !errors.As(err, &input) {
		t.Fatalf("CallTool error = %v, want *InputRequiredError", err)
	}
}

func TestElicitationOmitsAbsentRequestState(t *testing.T) {
	t.Parallel()

	calls := 0
	transport := &funcTransport{handle: modernHandler(t, func(method string, id json.RawMessage, _ json.RawMessage) [][]byte {
		if method != "tools/call" {
			return nil
		}
		calls++
		if calls == 1 {
			return [][]byte{elicitationReply(t, id, formRequest, "")}
		}
		return [][]byte{serverReply(id, `{"resultType":"complete","content":[]}`)}
	})}
	client := NewClient(transport)
	client.OnElicitation(func(context.Context, Elicitation) (ElicitationResult, error) {
		return ElicitationResult{Action: ElicitationAccept, Content: json.RawMessage(`{"name":"Ada"}`)}, nil
	})
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	if _, err := client.CallTool(context.Background(), "echo", nil); err != nil {
		t.Fatalf("CallTool error = %v", err)
	}
	params := sentFor(t, transport, "tools/call")
	var retry map[string]json.RawMessage
	if err := json.Unmarshal(params[1], &retry); err != nil {
		t.Fatalf("retry params %s: %v", params[1], err)
	}
	if _, ok := retry["requestState"]; ok {
		t.Errorf("retry carries requestState %s, want none", retry["requestState"])
	}
}
