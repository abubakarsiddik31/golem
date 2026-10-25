package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// legacyProtocolVersion is the MCP revision this client requests in
	// the initialize handshake. The server's reply is recorded as-is.
	legacyProtocolVersion = "2025-06-18"
	// modernProtocolVersion is the stateless MCP revision this client
	// prefers: per-request _meta instead of an initialize handshake.
	modernProtocolVersion = "2026-07-28"
	// unsupportedProtocolVersionCode is the JSON-RPC code a modern
	// server returns when it does not implement the requested revision.
	unsupportedProtocolVersionCode = -32022
	// progressNotification is the method of a server's progress
	// notification.
	progressNotification = "notifications/progress"
	// elicitationCreate is the method of an elicitation request inside
	// a modern input_required result.
	elicitationCreate = "elicitation/create"
	// maxInputRounds bounds multi-round-trip retries so a server that
	// keeps asking for input cannot spin one call forever.
	maxInputRounds = 8
	// probeTimeout bounds era detection so a silent legacy stdio server
	// cannot stall Initialize indefinitely. A late probe response is
	// dropped because it carries the probe's request id.
	probeTimeout  = 5 * time.Second
	clientName    = "golem"
	clientVersion = "0"
)

// UnsupportedProtocolVersionError reports a modern MCP server that does
// not implement the revision this client requested. Initialize returns
// it instead of falling back to the legacy handshake, because a
// recognized modern error means the server is not legacy.
type UnsupportedProtocolVersionError struct {
	// Requested is the revision the client sent.
	Requested string
	// Supported lists the revisions the server advertised; it may be
	// empty when the server omitted the list.
	Supported []string
}

func (e *UnsupportedProtocolVersionError) Error() string {
	if len(e.Supported) == 0 {
		return fmt.Sprintf("mcp: server does not support protocol version %s", e.Requested)
	}
	return fmt.Sprintf("mcp: server does not support protocol version %s (supported: %s)", e.Requested, strings.Join(e.Supported, ", "))
}

// InputRequiredError reports a modern result that asks the client for
// input this client cannot provide: an elicitation when no handler is
// installed, or a request kind it does not implement (sampling and
// roots, both deprecated by the 2026-07-28 revision).
type InputRequiredError struct {
	// Result is the raw input_required result the server returned.
	Result json.RawMessage
	// Method is the unsupported request's method, when one could be
	// identified; it is empty otherwise.
	Method string
}

func (e *InputRequiredError) Error() string {
	if e.Method != "" {
		return fmt.Sprintf("mcp: server requested %s, which this client does not support", e.Method)
	}
	return "mcp: server requested client input this client cannot provide"
}

// Elicitation modes.
const (
	// ElicitationModeForm collects data in band through the client.
	ElicitationModeForm = "form"
	// ElicitationModeURL sends the user to a URL for an out-of-band
	// interaction the client must not observe.
	ElicitationModeURL = "url"
)

// Elicitation response actions.
const (
	// ElicitationAccept means the user approved and submitted.
	ElicitationAccept = "accept"
	// ElicitationDecline means the user explicitly declined.
	ElicitationDecline = "decline"
	// ElicitationCancel means the user dismissed the request.
	ElicitationCancel = "cancel"
)

// Elicitation is one request from a server for information it cannot
// proceed without. It arrives inside a modern input_required result and
// is answered by the handler installed with OnElicitation.
type Elicitation struct {
	// Message explains why the server needs the input. It is written
	// for the person answering, not for the model.
	Message string
	// Mode is ElicitationModeForm or ElicitationModeURL. A request
	// that omits the mode is form mode.
	Mode string
	// RequestedSchema is the form mode's flat JSON Schema. Golem does
	// not validate the answer against it.
	RequestedSchema json.RawMessage
	// URL is the url mode's address. Golem never fetches or opens it;
	// the handler decides, after showing the full URL to the user.
	URL string
}

// ElicitationResult is the application's answer to one elicitation.
type ElicitationResult struct {
	// Action is ElicitationAccept, ElicitationDecline, or
	// ElicitationCancel.
	Action string
	// Content is the submitted form data, sent only for an accepted
	// form elicitation. It must be a JSON object; Golem does not
	// validate it against RequestedSchema.
	Content json.RawMessage
}

// requestMeta is the per-request protocol metadata a modern MCP
// revision requires. The keys are the specification's _meta names.
type requestMeta struct {
	ProtocolVersion    string             `json:"io.modelcontextprotocol/protocolVersion"`
	ClientInfo         ServerInfo         `json:"io.modelcontextprotocol/clientInfo"`
	ClientCapabilities clientCapabilities `json:"io.modelcontextprotocol/clientCapabilities"`
	// ProgressToken asks the server to report progress for this request.
	// It is zero when no handler is installed.
	ProgressToken int64 `json:"progressToken,omitempty"`
}

// clientCapabilities is the per-request capability object a modern
// request declares. An empty object means no optional capabilities.
type clientCapabilities struct {
	Elicitation *elicitationCapability `json:"elicitation,omitempty"`
}

// elicitationCapability declares both elicitation modes. The empty
// objects are the specification's shape: presence, not settings.
type elicitationCapability struct {
	Form struct{} `json:"form"`
	URL  struct{} `json:"url"`
}

// discoverResult is the response to server/discover. Server identity
// may be top-level or carried in _meta.
type discoverResult struct {
	SupportedVersions []string   `json:"supportedVersions"`
	ServerInfo        ServerInfo `json:"serverInfo"`
	Meta              struct {
		ServerInfo ServerInfo `json:"io.modelcontextprotocol/serverInfo"`
	} `json:"_meta"`
}

func (d discoverResult) server() ServerInfo {
	if d.Meta.ServerInfo.Name != "" {
		return d.Meta.ServerInfo
	}
	return d.ServerInfo
}

// ServerInfo identifies the server, as reported in its initialize
// response or in a modern discovery result's _meta.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ToolInfo is one tool a server offers, as reported by tools/list.
type ToolInfo struct {
	// Name is the tool's programmatic name; it is what CallTool takes.
	Name string `json:"name"`
	// Title is the human-readable name, when the server supplies one.
	Title string `json:"title,omitempty"`
	// Description explains what the tool does.
	Description string `json:"description"`
	// InputSchema is the JSON Schema of the arguments object. It is the
	// model-facing contract and is passed through verbatim.
	InputSchema json.RawMessage `json:"inputSchema"`
	// OutputSchema is the JSON Schema of a structured result, when the
	// server declares one. It is advisory: a tool may still answer with
	// text only.
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
	// Annotations are the server's behavioral hints about the tool.
	Annotations ToolAnnotations `json:"annotations,omitempty"`
}

// ToolAnnotations carries a server's behavioral hints about one tool.
// They are hints, not guarantees, and Golem never enforces them; an
// approval flow may read them to decide what to ask before a call.
//
// Each hint is a pointer because the protocol distinguishes "the server
// did not declare it" from an explicit false. Where the specification
// gives a default — destructive and open-world default to true,
// read-only and idempotent to false — nil means the default applies.
type ToolAnnotations struct {
	// Title is the human-readable name for the tool.
	Title string `json:"title,omitempty"`
	// ReadOnlyHint reports that the tool does not modify its
	// environment.
	ReadOnlyHint *bool `json:"readOnlyHint,omitempty"`
	// DestructiveHint reports that the tool may perform destructive
	// updates. The protocol's default is true.
	DestructiveHint *bool `json:"destructiveHint,omitempty"`
	// IdempotentHint reports that repeated identical calls have no
	// additional effect.
	IdempotentHint *bool `json:"idempotentHint,omitempty"`
	// OpenWorldHint reports that the tool interacts with an open
	// world — external entities beyond the server's own data. The
	// protocol's default is true.
	OpenWorldHint *bool `json:"openWorldHint,omitempty"`
}

// ContentItem is one piece of a tool result. Only text carries
// model-readable content; other types render as placeholders in
// CallResult.Text.
type ContentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// CallResult is the outcome of one tools/call.
type CallResult struct {
	// Content holds the result's content items in server order.
	Content []ContentItem `json:"content"`
	// StructuredContent is the result's JSON payload when the tool
	// declares an output schema. It is server-produced and therefore
	// untrusted; an application that relies on it validates it against
	// the matching ToolInfo.OutputSchema. Text falls back to it when
	// the result carries no content.
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	// IsError reports that the tool ran and failed; its text explains
	// why. The protocol error signal, not a Go error.
	IsError bool `json:"isError"`
}

// Text renders the result's content as tool result text: text items
// joined by newlines, one placeholder line per non-text item. When the
// result carries no content but does carry structured content — a tool
// with an output schema answering in JSON only — Text returns that
// JSON verbatim, so the model still sees the result.
func (r CallResult) Text() string {
	var parts []string
	for _, item := range r.Content {
		if item.Type == "text" {
			parts = append(parts, item.Text)
			continue
		}
		parts = append(parts, fmt.Sprintf("[mcp: unsupported %s content]", item.Type))
	}
	text := strings.Join(parts, "\n")
	if text == "" && len(r.StructuredContent) > 0 {
		return string(r.StructuredContent)
	}
	return text
}

// Progress is one update a server sent for the request in flight. It is
// delivered to the handler installed with OnProgress, on the goroutine
// that made the call.
type Progress struct {
	// Completed is the amount completed so far.
	Completed float64
	// Total is the expected total, or zero when the server did not
	// report one.
	Total float64
	// Message is an optional human-readable update.
	Message string
}

// Client speaks MCP to one server over a Transport. It is
// synchronous: each operation sends a request and waits for its
// response, so operations serialize behind an internal mutex and no
// goroutines are introduced. A Client is safe for concurrent use.
//
// A Client is dual-era: Initialize probes for the stateless modern
// revision and falls back to the initialize handshake for legacy
// servers. The negotiated revision decides whether requests carry
// per-request _meta and whether server-initiated requests are legal.
type Client struct {
	transport     Transport
	mu            sync.Mutex
	nextID        int64
	server        ServerInfo
	version       string
	modern        bool
	ready         bool
	progress      *progressHandler
	elicitation   *elicitationHandler
	nextProgress  int64
	progressToken int64
}

// progressHandler holds the installed progress callback. It is a pointer
// so Client stays comparable; the client never copies it.
type progressHandler struct {
	fn func(Progress)
}

// elicitationHandler holds the installed elicitation callback. It is a
// pointer so Client stays comparable; the client never copies it.
type elicitationHandler struct {
	fn func(context.Context, Elicitation) (ElicitationResult, error)
}

// NewClient returns a Client speaking to t. Call Initialize before
// any other operation.
func NewClient(t Transport) *Client {
	return &Client{transport: t}
}

// Initialize negotiates the protocol era, then announces this client
// and records the server's identity. It probes for the modern
// stateless revision first and falls back to the legacy initialize
// handshake when the server does not answer server/discover. A second
// call is a no-op.
func (c *Client) Initialize(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ready {
		return nil
	}
	if err := c.detectEra(ctx); err != nil {
		return err
	}
	if !c.modern {
		if err := c.initializeLegacy(ctx); err != nil {
			return err
		}
	}
	c.ready = true
	return nil
}

// detectEra probes server/discover to decide which revision the server
// speaks. A successful discovery is the modern era; a recognized
// unsupported-version error is reported rather than retried as legacy,
// because only a modern server produces it. Every other outcome — a
// method-not-found error, an HTTP error, or a timeout — means the
// server is legacy and the initialize handshake decides.
//
// The caller holds c.mu.
func (c *Client) detectEra(ctx context.Context) error {
	// Modern metadata is attached only while the probe is in flight, so
	// the legacy handshake that may follow stays legacy.
	c.modern = true
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	var discovered discoverResult
	err := c.call(probeCtx, "server/discover", nil, &discovered)
	switch {
	case err == nil:
		if !supportsVersion(discovered.SupportedVersions, modernProtocolVersion) {
			return &UnsupportedProtocolVersionError{
				Requested: modernProtocolVersion,
				Supported: discovered.SupportedVersions,
			}
		}
		c.version = modernProtocolVersion
		c.server = discovered.server()
		return nil
	}

	if unsupported := unsupportedFromError(err); unsupported != nil {
		return unsupported
	}
	if unsupported := unsupportedFromBody(errorBody(c.transport)); unsupported != nil {
		return unsupported
	}

	// A cancelled or expired caller context is a real failure, not a
	// legacy server: do not fall back and mask it.
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("mcp: detect protocol era: %w", err)
	}

	// Credentials were refused. Falling back to the legacy handshake
	// would only be refused too, and it would bury the challenge the
	// application needs to re-authorize.
	var unauthorized *AuthError
	if errors.As(err, &unauthorized) {
		return err
	}

	c.modern = false
	c.version = legacyProtocolVersion
	return nil
}

// initializeLegacy performs the initialize handshake for a legacy
// server and completes it with the initialized notification.
//
// The caller holds c.mu.
func (c *Client) initializeLegacy(ctx context.Context) error {
	params := struct {
		ProtocolVersion string     `json:"protocolVersion"`
		Capabilities    struct{}   `json:"capabilities"`
		ClientInfo      ServerInfo `json:"clientInfo"`
	}{
		ProtocolVersion: legacyProtocolVersion,
		ClientInfo:      ServerInfo{Name: clientName, Version: clientVersion},
	}
	var result struct {
		ProtocolVersion string     `json:"protocolVersion"`
		ServerInfo      ServerInfo `json:"serverInfo"`
	}
	if err := c.call(ctx, "initialize", params, &result); err != nil {
		return err
	}
	if err := c.notify(ctx, "notifications/initialized", nil); err != nil {
		return fmt.Errorf("mcp: send initialized notification: %w", err)
	}
	c.server = result.ServerInfo
	if result.ProtocolVersion != "" {
		c.version = result.ProtocolVersion
	}
	return nil
}

// Server returns the server's reported identity; meaningful after
// Initialize.
func (c *Client) Server() ServerInfo {
	return c.server
}

// ProtocolVersion returns the negotiated MCP revision: the modern
// stateless revision, or the legacy revision the server confirmed in
// its initialize response. It is empty before Initialize.
func (c *Client) ProtocolVersion() string {
	return c.version
}

// OnProgress installs a handler for the progress notifications a server
// sends while a request is in flight. With a handler installed, every
// request asks for progress by carrying a fresh token in its metadata;
// the handler runs inline on the goroutine that made the call, while the
// client holds its lock, so it must be quick and must not call back into
// this client. Passing nil removes the handler and stops requesting
// progress.
func (c *Client) OnProgress(handler func(Progress)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if handler == nil {
		c.progress = nil
		return
	}
	c.progress = &progressHandler{fn: handler}
}

// OnElicitation installs a handler for the elicitation requests a modern
// server sends inside an input_required result. With a handler
// installed, every modern request declares the elicitation capability in
// its metadata, so a compliant server may ask for input. The handler
// runs inline on the goroutine that made the call, while the client
// holds its lock: it may block on the caller's context, but it must not
// call back into this client. Passing nil removes the handler and stops
// declaring the capability.
//
// A handler that returns an error fails the call. A decline or cancel is
// sent to the server, which decides what the call does next. For url mode
// the handler must show the full URL and obtain explicit consent before
// returning accept; Golem never fetches or opens the URL.
func (c *Client) OnElicitation(handler func(context.Context, Elicitation) (ElicitationResult, error)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if handler == nil {
		c.elicitation = nil
		return
	}
	c.elicitation = &elicitationHandler{fn: handler}
}

// Close closes the underlying transport, terminating the server
// connection or process.
func (c *Client) Close() error {
	return c.transport.Close()
}

// errNotInitialized is the failure of an operation attempted before
// Initialize.
var errNotInitialized = errors.New("mcp: client is not initialized")

// paginate walks one list method's cursor pages, calling fetch with each
// cursor — the empty string on the first call — and appending every
// page's items until the server stops returning a next cursor.
func paginate[T any](ctx context.Context, fetch func(cursor string) ([]T, string, error)) ([]T, error) {
	var items []T
	var cursor string
	for {
		page, next, err := fetch(cursor)
		if err != nil {
			return nil, err
		}
		items = append(items, page...)
		if next == "" {
			return items, nil
		}
		cursor = next
	}
}

// ListTools returns the server's complete tool list, following
// pagination cursors until the list is exhausted.
func (c *Client) ListTools(ctx context.Context) ([]ToolInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.ready {
		return nil, errNotInitialized
	}
	return paginate(ctx, func(cursor string) ([]ToolInfo, string, error) {
		var result struct {
			Tools      []ToolInfo `json:"tools"`
			NextCursor string     `json:"nextCursor"`
		}
		if err := c.call(ctx, "tools/list", pageCursor{Cursor: cursor}, &result); err != nil {
			return nil, "", err
		}
		return result.Tools, result.NextCursor, nil
	})
}

// pageCursor is the params of one list request's page. An empty cursor
// marshals to an empty object, which is what a first page wants.
type pageCursor struct {
	Cursor string `json:"cursor,omitempty"`
}

// CallTool executes one tool on the server with raw model-produced
// arguments. The caller's context bounds the call.
func (c *Client) CallTool(ctx context.Context, name string, arguments json.RawMessage) (CallResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.ready {
		return CallResult{}, errNotInitialized
	}
	params := struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments,omitempty"`
	}{Name: name, Arguments: arguments}
	var result CallResult
	if err := c.call(ctx, "tools/call", params, &result); err != nil {
		return CallResult{}, err
	}
	return result, nil
}

// call sends one request and reads until its final response arrives. In
// the modern era it also answers the elicitation requests inside an
// input_required result and retries, echoing the server's state. The
// caller holds c.mu.
func (c *Client) call(ctx context.Context, method string, params any, result any) error {
	base, err := marshalParams(method, params)
	if err != nil {
		return err
	}
	for round := 0; ; round++ {
		raw, err := c.exchange(ctx, method, base, params != nil)
		if err != nil {
			return err
		}
		required, err := inputRequired(method, raw, c.modern)
		if err != nil {
			return err
		}
		if required == nil {
			return decodeResult(method, raw, result)
		}
		if round == maxInputRounds {
			return fmt.Errorf("mcp: %s still needs input after %d rounds", method, maxInputRounds)
		}
		if err := c.answerInputs(ctx, base, required); err != nil {
			return err
		}
	}
}

// exchange sends one request and reads until its response arrives,
// answering legacy server-initiated requests inline and dropping
// notifications. It returns the raw result, leaving input_required
// handling to the caller. The caller holds c.mu.
func (c *Client) exchange(ctx context.Context, method string, base map[string]json.RawMessage, present bool) (json.RawMessage, error) {
	c.nextID++
	c.progressToken = 0
	if c.progress != nil {
		c.nextProgress++
		c.progressToken = c.nextProgress
	}
	request, err := c.withMeta(method, base)
	if err != nil {
		return nil, err
	}
	id, err := json.Marshal(c.nextID)
	if err != nil {
		return nil, fmt.Errorf("mcp: encode request id: %w", err)
	}
	message := rpcMessage{JSONRPC: "2.0", ID: id, Method: method}
	if present || len(request) > 0 {
		encoded, err := json.Marshal(request)
		if err != nil {
			return nil, fmt.Errorf("mcp: encode %s params: %w", method, err)
		}
		message.Params = encoded
	}
	if err := c.send(ctx, message, method); err != nil {
		return nil, err
	}

	for {
		raw, err := c.transport.Read(ctx)
		if err != nil {
			return nil, fmt.Errorf("mcp: await %s: %w", method, err)
		}
		var incoming rpcMessage
		if err := json.Unmarshal(raw, &incoming); err != nil {
			return nil, fmt.Errorf("mcp: decode message: %w", err)
		}
		switch {
		case incoming.Method != "" && len(incoming.ID) > 0:
			if c.modern {
				// Modern sessions have no server-initiated requests:
				// client features arrive as input requests inside a
				// result instead. Answering would corrupt the session.
				return nil, fmt.Errorf("mcp: server sent request %q in a stateless session", incoming.Method)
			}
			if err := c.answer(ctx, incoming.ID, incoming.Method); err != nil {
				return nil, err
			}
		case incoming.Method != "":
			// A notification. Progress is the only one this client
			// consumes; everything else is dropped.
			if incoming.Method == progressNotification {
				c.deliverProgress(incoming.Params)
			}
		case len(incoming.ID) > 0:
			if !sameID(incoming.ID, id) {
				// A late response to a call that already gave up;
				// the serialized client never expects it, but
				// dropping it is the safe move.
				continue
			}
			if incoming.Error != nil {
				return nil, &ProtocolError{
					Code:    incoming.Error.Code,
					Message: incoming.Error.Message,
					Data:    incoming.Error.Data,
				}
			}
			if len(incoming.Result) == 0 {
				return nil, fmt.Errorf("mcp: %s response has no result", method)
			}
			return incoming.Result, nil
		default:
			return nil, fmt.Errorf("mcp: malformed message: %s", raw)
		}
	}
}

// inputRequired reports whether a result is a modern input_required
// envelope, returning the raw result when it is and nil when the request
// is complete. A legacy result is never an input request.
func inputRequired(method string, raw json.RawMessage, modern bool) (json.RawMessage, error) {
	if !modern {
		return nil, nil
	}
	var envelope struct {
		ResultType string `json:"resultType"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("mcp: decode %s result: %w", method, err)
	}
	switch envelope.ResultType {
	case "", "complete":
		return nil, nil
	case "input_required":
		return raw, nil
	default:
		return nil, fmt.Errorf("mcp: %s result has unknown resultType %q", method, envelope.ResultType)
	}
}

// decodeResult unmarshals a complete result into result.
func decodeResult(method string, raw json.RawMessage, result any) error {
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(raw, result); err != nil {
		return fmt.Errorf("mcp: decode %s result: %w", method, err)
	}
	return nil
}

// inputRequiredResult is a modern input_required result: the requests the
// client must answer before retrying, plus the opaque state it must echo.
type inputRequiredResult struct {
	InputRequests map[string]json.RawMessage `json:"inputRequests"`
	RequestState  json.RawMessage            `json:"requestState"`
}

// answerInputs answers one input_required result, adding inputResponses
// and requestState to the retry's parameters. It fails with
// *InputRequiredError when it cannot answer: no handler is installed, a
// request kind is unsupported, or the result asks for nothing at all. The
// caller holds c.mu.
func (c *Client) answerInputs(ctx context.Context, base map[string]json.RawMessage, raw json.RawMessage) error {
	if c.elicitation == nil {
		// Without a handler nothing can be answered, and a malformed
		// shape must still surface as the typed error rather than a
		// decode failure.
		return &InputRequiredError{Result: raw, Method: bestEffortRequestMethod(raw)}
	}
	var required inputRequiredResult
	if err := json.Unmarshal(raw, &required); err != nil {
		return fmt.Errorf("mcp: decode input_required result: %w", err)
	}
	// Each round replaces the previous round's answers and state.
	delete(base, "inputResponses")
	delete(base, "requestState")
	if len(required.RequestState) > 0 {
		base["requestState"] = required.RequestState
	}
	if len(required.InputRequests) == 0 {
		// The server asked only for a retry with its state.
		if len(required.RequestState) == 0 {
			return &InputRequiredError{Result: raw}
		}
		return nil
	}
	responses := make(map[string]json.RawMessage, len(required.InputRequests))
	for _, key := range sortedKeys(required.InputRequests) {
		response, err := c.answerInput(ctx, required.InputRequests[key], raw)
		if err != nil {
			return err
		}
		responses[key] = response
	}
	encoded, err := json.Marshal(responses)
	if err != nil {
		return fmt.Errorf("mcp: encode input responses: %w", err)
	}
	base["inputResponses"] = encoded
	return nil
}

// answerInput answers one server request. Elicitation goes to the
// installed handler; every other kind is reported as unsupported. result
// is the whole input_required result, for the typed error.
func (c *Client) answerInput(ctx context.Context, raw json.RawMessage, result json.RawMessage) (json.RawMessage, error) {
	var request struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, fmt.Errorf("mcp: decode input request: %w", err)
	}
	if request.Method != elicitationCreate {
		return nil, &InputRequiredError{Result: result, Method: request.Method}
	}
	var params struct {
		Mode            string          `json:"mode"`
		Message         string          `json:"message"`
		RequestedSchema json.RawMessage `json:"requestedSchema"`
		URL             string          `json:"url"`
	}
	if err := json.Unmarshal(request.Params, &params); err != nil {
		return nil, fmt.Errorf("mcp: decode elicitation request: %w", err)
	}
	mode := params.Mode
	if mode == "" {
		// The protocol treats an omitted mode as form mode.
		mode = ElicitationModeForm
	}
	answer, err := c.elicitation.fn(ctx, Elicitation{
		Message:         params.Message,
		Mode:            mode,
		RequestedSchema: params.RequestedSchema,
		URL:             params.URL,
	})
	if err != nil {
		return nil, fmt.Errorf("mcp: elicitation handler: %w", err)
	}
	switch answer.Action {
	case ElicitationAccept, ElicitationDecline, ElicitationCancel:
	default:
		return nil, fmt.Errorf("mcp: elicitation handler returned unknown action %q", answer.Action)
	}
	response := elicitResult{Action: answer.Action}
	if answer.Action == ElicitationAccept && mode == ElicitationModeForm {
		response.Content = answer.Content
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("mcp: encode elicitation response: %w", err)
	}
	return encoded, nil
}

// elicitResult is the client's answer to one elicitation request.
type elicitResult struct {
	Action  string          `json:"action"`
	Content json.RawMessage `json:"content,omitempty"`
}

// bestEffortRequestMethod names the first identifiable input request for
// error reporting, tolerating a shape it cannot parse.
func bestEffortRequestMethod(raw json.RawMessage) string {
	var envelope struct {
		InputRequests map[string]json.RawMessage `json:"inputRequests"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return ""
	}
	for _, key := range sortedKeys(envelope.InputRequests) {
		var request struct {
			Method string `json:"method"`
		}
		if json.Unmarshal(envelope.InputRequests[key], &request) == nil && request.Method != "" {
			return request.Method
		}
	}
	return ""
}

// sortedKeys returns the map's keys in order, so work over a
// server-assigned map is deterministic.
func sortedKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// deliverProgress hands one progress notification to the installed
// handler, dropping it when no handler is set or when the token does not
// match the request in flight — a late notification from an earlier call
// must not be reported as the current one. The caller holds c.mu.
func (c *Client) deliverProgress(params json.RawMessage) {
	if c.progress == nil {
		return
	}
	var note struct {
		ProgressToken json.RawMessage `json:"progressToken"`
		Progress      float64         `json:"progress"`
		Total         float64         `json:"total"`
		Message       string          `json:"message"`
	}
	if err := json.Unmarshal(params, &note); err != nil {
		return
	}
	if canonicalJSON(note.ProgressToken) != strconv.FormatInt(c.progressToken, 10) {
		return
	}
	c.progress.fn(Progress{Completed: note.Progress, Total: note.Total, Message: note.Message})
}

// marshalParams encodes a request's parameters into an object so a retry
// round can add fields. A request with no parameters becomes an empty
// object.
func marshalParams(method string, params any) (map[string]json.RawMessage, error) {
	object := make(map[string]json.RawMessage)
	if params == nil {
		return object, nil
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("mcp: encode %s params: %w", method, err)
	}
	if err := json.Unmarshal(encoded, &object); err != nil {
		return nil, fmt.Errorf("mcp: encode %s params: %w", method, err)
	}
	return object, nil
}

// withMeta copies base and adds the per-request metadata: the modern
// revision's required fields, or a legacy request's progress token. The
// caller holds c.mu.
func (c *Client) withMeta(method string, base map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	request := make(map[string]json.RawMessage, len(base)+1)
	for key, value := range base {
		request[key] = value
	}
	if c.modern || c.progress != nil {
		meta, err := json.Marshal(c.meta())
		if err != nil {
			return nil, fmt.Errorf("mcp: encode %s metadata: %w", method, err)
		}
		request["_meta"] = meta
	}
	return request, nil
}

// meta builds the per-request metadata: the modern revision's required
// fields, plus the progress token when a handler is installed. A legacy
// request carries only the token.
//
// The caller holds c.mu.
func (c *Client) meta() any {
	if c.modern {
		return requestMeta{
			ProtocolVersion:    modernProtocolVersion,
			ClientInfo:         ServerInfo{Name: clientName, Version: clientVersion},
			ClientCapabilities: c.capabilities(),
			ProgressToken:      c.progressToken,
		}
	}
	return struct {
		ProgressToken int64 `json:"progressToken"`
	}{ProgressToken: c.progressToken}
}

// capabilities reports the client features this session declares. A
// modern request always carries the object; an empty one means no
// optional capabilities.
//
// The caller holds c.mu.
func (c *Client) capabilities() clientCapabilities {
	if c.elicitation == nil {
		return clientCapabilities{}
	}
	return clientCapabilities{Elicitation: &elicitationCapability{}}
}

// supportsVersion reports whether the server listed the revision the
// client wants. An empty list cannot be read as a refusal: the server
// answered discovery, so the era is settled and the revision is the
// client's choice.
func supportsVersion(versions []string, want string) bool {
	if len(versions) == 0 {
		return true
	}
	for _, version := range versions {
		if version == want {
			return true
		}
	}
	return false
}

// unsupportedFromError turns a modern unsupported-version JSON-RPC
// error into its typed form. It returns nil for any other error.
func unsupportedFromError(err error) *UnsupportedProtocolVersionError {
	var protocolErr *ProtocolError
	if !errors.As(err, &protocolErr) || protocolErr.Code != unsupportedProtocolVersionCode {
		return nil
	}
	return unsupportedFromProtocolError(protocolErr)
}

// unsupportedFromProtocolError decodes the error's data, which carries
// the revisions the server does support and the one it refused.
func unsupportedFromProtocolError(protocolErr *ProtocolError) *UnsupportedProtocolVersionError {
	unsupported := &UnsupportedProtocolVersionError{Requested: modernProtocolVersion}
	var data struct {
		Requested string   `json:"requested"`
		Supported []string `json:"supported"`
	}
	if len(protocolErr.Data) > 0 && json.Unmarshal(protocolErr.Data, &data) == nil {
		if data.Requested != "" {
			unsupported.Requested = data.Requested
		}
		unsupported.Supported = data.Supported
	}
	return unsupported
}

// unsupportedFromBody recognizes an unsupported-version error carried
// by an HTTP error response, which is how a modern server rejects a
// revision it does not implement.
func unsupportedFromBody(body []byte) *UnsupportedProtocolVersionError {
	if len(body) == 0 {
		return nil
	}
	var message rpcMessage
	if err := json.Unmarshal(body, &message); err != nil || message.Error == nil {
		return nil
	}
	if message.Error.Code != unsupportedProtocolVersionCode {
		return nil
	}
	return unsupportedFromProtocolError(&ProtocolError{
		Code:    message.Error.Code,
		Message: message.Error.Message,
		Data:    message.Error.Data,
	})
}

// errorBody returns the body of a transport's last HTTP error
// response, when the transport keeps one.
func errorBody(t Transport) []byte {
	reader, ok := t.(interface{ lastErrorBody() []byte })
	if !ok {
		return nil
	}
	return reader.lastErrorBody()
}

// notify sends one notification; there is no response to wait for.
// The caller holds c.mu.
func (c *Client) notify(ctx context.Context, method string, params any) error {
	request := rpcMessage{JSONRPC: "2.0", Method: method}
	if params != nil {
		encoded, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("mcp: encode %s params: %w", method, err)
		}
		request.Params = encoded
	}
	return c.send(ctx, request, method)
}

// answer replies to one server-initiated request: ping gets an empty
// result, anything else a method-not-found error. The caller holds
// c.mu.
func (c *Client) answer(ctx context.Context, id json.RawMessage, method string) error {
	var reply rpcMessage
	if method == "ping" {
		reply = rpcMessage{JSONRPC: "2.0", ID: id, Result: json.RawMessage("{}")}
	} else {
		reply = rpcMessage{
			JSONRPC: "2.0",
			ID:      id,
			Error:   &rpcError{Code: methodNotFound, Message: "method not found: " + method},
		}
	}
	return c.send(ctx, reply, "reply to "+method)
}

// send encodes and delivers one message.
func (c *Client) send(ctx context.Context, message rpcMessage, what string) error {
	encoded, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("mcp: encode %s: %w", what, err)
	}
	if err := c.transport.Send(ctx, encoded); err != nil {
		return fmt.Errorf("mcp: send %s: %w", what, err)
	}
	return nil
}
