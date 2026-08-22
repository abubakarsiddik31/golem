// Package mcp connects Golem agents to Model Context Protocol servers:
// it speaks the JSON-RPC 2.0 protocol over a transport, performs the
// initialize handshake, discovers a server's tools, and bridges them
// into tool.Tool declarations any agent can register.
//
// The client is synchronous — one in-flight request at a time — and
// introduces no goroutines. Decisions live in
// docs/adr/0016-mcp-client-package.md.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
)

// Transport carries JSON-RPC 2.0 messages, as JSON text, between the
// client and one MCP server. Implementations keep message framing:
// one Send delivers exactly one message; one Read returns exactly one.
type Transport interface {
	// Send delivers one message to the server. For transports where
	// delivery is a round trip (HTTP), the response becomes the next
	// message Read returns.
	Send(ctx context.Context, message []byte) error
	// Read returns the next message the transport received. It blocks
	// until one arrives, the context is done, or the transport fails.
	Read(ctx context.Context) ([]byte, error)
	// Close releases the transport's resources. It is idempotent
	// enough for defer.
	Close() error
}

// rpcMessage is one JSON-RPC 2.0 message in either direction. A
// request has Method and ID; a notification has Method only; a
// response has ID with Result or Error.
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError is the error member of a JSON-RPC response.
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// ProtocolError reports a JSON-RPC error response from the server:
// standard JSON-RPC codes (-32700..-32603) or an MCP-specific one.
// It is a protocol failure, not a tool failure — it surfaces at the
// tool stage and is never a correctable rejection.
type ProtocolError struct {
	// Code is the JSON-RPC error code.
	Code int
	// Message is the server's error message.
	Message string
	// Data carries the server's optional error data, raw JSON.
	Data json.RawMessage
}

func (e *ProtocolError) Error() string {
	if len(e.Data) > 0 {
		return fmt.Sprintf("mcp: json-rpc error %d: %s: %s", e.Code, e.Message, e.Data)
	}
	return fmt.Sprintf("mcp: json-rpc error %d: %s", e.Code, e.Message)
}

// methodNotFound is the JSON-RPC code for an unrecognized method.
const methodNotFound = -32601

// sameID compares two JSON-RPC ids through their canonical encoding,
// so 1, 1.0, and "1" each match only their own kind.
func sameID(a, b json.RawMessage) bool {
	return canonicalJSON(a) == canonicalJSON(b)
}

// canonicalJSON re-encodes v through a decode/encode round trip,
// normalizing whitespace and number formatting.
func canonicalJSON(v json.RawMessage) string {
	var decoded any
	if err := json.Unmarshal(v, &decoded); err != nil {
		return string(v)
	}
	encoded, err := json.Marshal(decoded)
	if err != nil {
		return string(v)
	}
	return string(encoded)
}
