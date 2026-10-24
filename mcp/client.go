package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// InputRequiredError reports a modern MCP result that asks the client
// for more input through multi-round-trip requests (sampling,
// elicitation, or roots). This client implements none of those client
// features, so the call fails instead of silently dropping the request.
type InputRequiredError struct {
	// Result is the raw result the server returned.
	Result json.RawMessage
}

func (e *InputRequiredError) Error() string {
	return "mcp: server requested client input, which this client does not support"
}

// requestMeta is the per-request protocol metadata a modern MCP
// revision requires. The keys are the specification's _meta names.
type requestMeta struct {
	ProtocolVersion    string     `json:"io.modelcontextprotocol/protocolVersion"`
	ClientInfo         ServerInfo `json:"io.modelcontextprotocol/clientInfo"`
	ClientCapabilities struct{}   `json:"io.modelcontextprotocol/clientCapabilities"`
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
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
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
	// IsError reports that the tool ran and failed; its text explains
	// why. The protocol error signal, not a Go error.
	IsError bool `json:"isError"`
}

// Text renders the result's content as tool result text: text items
// joined by newlines, one placeholder line per non-text item.
func (r CallResult) Text() string {
	var parts []string
	for _, item := range r.Content {
		if item.Type == "text" {
			parts = append(parts, item.Text)
			continue
		}
		parts = append(parts, fmt.Sprintf("[mcp: unsupported %s content]", item.Type))
	}
	return strings.Join(parts, "\n")
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
	transport Transport
	mu        sync.Mutex
	nextID    int64
	server    ServerInfo
	version   string
	modern    bool
	ready     bool
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

// Close closes the underlying transport, terminating the server
// connection or process.
func (c *Client) Close() error {
	return c.transport.Close()
}

// ListTools returns the server's complete tool list, following
// pagination cursors until the list is exhausted.
func (c *Client) ListTools(ctx context.Context) ([]ToolInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.ready {
		return nil, errors.New("mcp: client is not initialized")
	}
	var tools []ToolInfo
	var params struct {
		Cursor string `json:"cursor,omitempty"`
	}
	for {
		var result struct {
			Tools      []ToolInfo `json:"tools"`
			NextCursor string     `json:"nextCursor"`
		}
		if err := c.call(ctx, "tools/list", params, &result); err != nil {
			return nil, err
		}
		tools = append(tools, result.Tools...)
		if result.NextCursor == "" {
			return tools, nil
		}
		params.Cursor = result.NextCursor
	}
}

// CallTool executes one tool on the server with raw model-produced
// arguments. The caller's context bounds the call.
func (c *Client) CallTool(ctx context.Context, name string, arguments json.RawMessage) (CallResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.ready {
		return CallResult{}, errors.New("mcp: client is not initialized")
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

// call sends one request and reads until its response arrives,
// answering server-initiated requests inline and dropping
// notifications. The caller holds c.mu.
func (c *Client) call(ctx context.Context, method string, params any, result any) error {
	c.nextID++
	id, err := json.Marshal(c.nextID)
	if err != nil {
		return fmt.Errorf("mcp: encode request id: %w", err)
	}
	request := rpcMessage{JSONRPC: "2.0", ID: id, Method: method}
	encoded, err := c.encodeParams(method, params)
	if err != nil {
		return err
	}
	request.Params = encoded
	if err := c.send(ctx, request, method); err != nil {
		return err
	}

	for {
		raw, err := c.transport.Read(ctx)
		if err != nil {
			return fmt.Errorf("mcp: await %s: %w", method, err)
		}
		var message rpcMessage
		if err := json.Unmarshal(raw, &message); err != nil {
			return fmt.Errorf("mcp: decode message: %w", err)
		}
		switch {
		case message.Method != "" && len(message.ID) > 0:
			if c.modern {
				// Modern sessions have no server-initiated requests:
				// client features arrive as input requests inside a
				// result instead. Answering would corrupt the session.
				return fmt.Errorf("mcp: server sent request %q in a stateless session", message.Method)
			}
			if err := c.answer(ctx, message.ID, message.Method); err != nil {
				return err
			}
		case message.Method != "":
			// A notification; nothing to answer.
		case len(message.ID) > 0:
			if !sameID(message.ID, id) {
				// A late response to a call that already gave up;
				// the serialized client never expects it, but
				// dropping it is the safe move.
				continue
			}
			if message.Error != nil {
				return &ProtocolError{
					Code:    message.Error.Code,
					Message: message.Error.Message,
					Data:    message.Error.Data,
				}
			}
			if len(message.Result) == 0 {
				if result != nil {
					return fmt.Errorf("mcp: %s response has no result", method)
				}
				return nil
			}
			if c.modern {
				if err := checkResultType(method, message.Result); err != nil {
					return err
				}
			}
			if result != nil {
				if err := json.Unmarshal(message.Result, result); err != nil {
					return fmt.Errorf("mcp: decode %s result: %w", method, err)
				}
			}
			return nil
		default:
			return fmt.Errorf("mcp: malformed message: %s", raw)
		}
	}
}

// encodeParams marshals one request's params. In the modern era it also
// adds the required per-request _meta, creating an object for a request
// that has no other params. A legacy request keeps its params verbatim.
//
// The caller holds c.mu.
func (c *Client) encodeParams(method string, params any) (json.RawMessage, error) {
	var object map[string]json.RawMessage
	if params != nil {
		encoded, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("mcp: encode %s params: %w", method, err)
		}
		if err := json.Unmarshal(encoded, &object); err != nil {
			return nil, fmt.Errorf("mcp: encode %s params: %w", method, err)
		}
	}
	if c.modern {
		if object == nil {
			object = make(map[string]json.RawMessage, 1)
		}
		meta, err := json.Marshal(newRequestMeta())
		if err != nil {
			return nil, fmt.Errorf("mcp: encode %s metadata: %w", method, err)
		}
		object["_meta"] = meta
	}
	if object == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("mcp: encode %s params: %w", method, err)
	}
	return encoded, nil
}

// newRequestMeta builds the per-request metadata a modern revision
// requires: the revision, this client's identity, and its (empty)
// capabilities.
func newRequestMeta() requestMeta {
	return requestMeta{
		ProtocolVersion:    modernProtocolVersion,
		ClientInfo:         ServerInfo{Name: clientName, Version: clientVersion},
		ClientCapabilities: struct{}{},
	}
}

// checkResultType enforces the modern result envelope. A missing type
// means a complete result; an input request is the multi-round-trip
// mechanism this client does not implement; anything else is invalid.
func checkResultType(method string, result json.RawMessage) error {
	var envelope struct {
		ResultType string `json:"resultType"`
	}
	if err := json.Unmarshal(result, &envelope); err != nil {
		return fmt.Errorf("mcp: decode %s result: %w", method, err)
	}
	switch envelope.ResultType {
	case "", "complete":
		return nil
	case "input_required":
		return &InputRequiredError{Result: result}
	default:
		return fmt.Errorf("mcp: %s result has unknown resultType %q", method, envelope.ResultType)
	}
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
