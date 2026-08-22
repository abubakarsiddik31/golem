package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
)

const (
	// protocolVersion is the MCP revision this client speaks in the
	// initialize handshake; the server's reply is recorded as-is.
	protocolVersion = "2025-06-18"
	clientName      = "golem"
	clientVersion   = "0"
)

// ServerInfo identifies the server, as reported in its initialize
// response.
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
type Client struct {
	transport Transport
	mu        sync.Mutex
	nextID    int64
	server    ServerInfo
	ready     bool
}

// NewClient returns a Client speaking to t. Call Initialize before
// any other operation.
func NewClient(t Transport) *Client {
	return &Client{transport: t}
}

// Initialize performs the MCP initialize handshake: it announces this
// client, records the server's identity, and completes the
// initialized notification. A second call is a no-op.
func (c *Client) Initialize(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ready {
		return nil
	}
	params := struct {
		ProtocolVersion string     `json:"protocolVersion"`
		Capabilities    struct{}   `json:"capabilities"`
		ClientInfo      ServerInfo `json:"clientInfo"`
	}{
		ProtocolVersion: protocolVersion,
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
	c.ready = true
	return nil
}

// Server returns the server's reported identity; meaningful after
// Initialize.
func (c *Client) Server() ServerInfo {
	return c.server
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
	if params != nil {
		encoded, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("mcp: encode %s params: %w", method, err)
		}
		request.Params = encoded
	}
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
			if result != nil {
				if len(message.Result) == 0 {
					return fmt.Errorf("mcp: %s response has no result", method)
				}
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
