// Package mcpserver exposes one Golem agent as a Model Context Protocol
// server: MCP clients see a single tool, call it with a prompt, and
// receive the agent's formatted answer. The server speaks JSON-RPC 2.0
// over any mcp.Transport, ships a stdio transport, and provides an
// http.Handler for the stateless streamable-HTTP request shape.
//
// The server is transport-agnostic and sequential per transport. The
// HTTP handler is safe for concurrent requests because the wrapped agent
// is; applications own authentication, authorization, and per-caller
// dependency resolution.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/mcp"
)

// Protocol revisions the server understands. It echoes a client's
// requested revision when it is one of these, and otherwise answers with
// the legacy revision.
const (
	LegacyProtocolVersion = "2025-06-18"
	ModernProtocolVersion = "2026-07-28"
)

// maxRequestBody bounds one HTTP JSON-RPC message.
const maxRequestBody = 1 << 20

// Config configures one exposed agent tool. Name and Agent are required.
type Config[Deps any, Output any] struct {
	// Name is the MCP tool name clients call.
	Name string
	// Description tells the client when to use the tool.
	Description string
	// Version is the serverInfo version; empty selects "0".
	Version string
	// InputSchema describes the tool arguments. Empty selects a schema
	// with one required "prompt" string. A non-empty schema must be a
	// valid JSON object.
	InputSchema json.RawMessage
	// Prompt turns the call's arguments into the agent prompt. Nil reads
	// the default schema's "prompt" string.
	Prompt func(args json.RawMessage) (string, error)
	// Format turns the agent output into the tool result text. Nil uses
	// a string output as-is and JSON-encodes other values.
	Format func(output Output) (string, error)
	// Deps resolves the dependency value for one call. Nil uses the
	// zero value. An error becomes a failed tool result, not a protocol
	// error, so the client model sees it.
	Deps func(ctx context.Context) (Deps, error)
}

// Server exposes one Golem agent as an MCP tool.
type Server[Deps any, Output any] struct {
	agent *golem.Agent[Deps, Output]
	cfg   Config[Deps, Output]
}

// New validates cfg and returns the server.
func New[Deps any, Output any](agent *golem.Agent[Deps, Output], cfg Config[Deps, Output]) (*Server[Deps, Output], error) {
	if agent == nil {
		return nil, fmt.Errorf("mcpserver: agent is required")
	}
	if strings.TrimSpace(cfg.Name) == "" {
		return nil, fmt.Errorf("mcpserver: tool name is required")
	}
	if len(cfg.InputSchema) == 0 {
		cfg.InputSchema = defaultSchema
	}
	if !json.Valid(cfg.InputSchema) {
		return nil, fmt.Errorf("mcpserver: input schema is not valid JSON")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(cfg.InputSchema, &object); err != nil {
		return nil, fmt.Errorf("mcpserver: input schema must be a JSON object: %w", err)
	}
	if cfg.Version == "" {
		cfg.Version = "0"
	}
	return &Server[Deps, Output]{agent: agent, cfg: cfg}, nil
}

// Serve reads JSON-RPC messages from transport until the context ends,
// the transport closes, or delivery fails. It is sequential: one request
// is handled at a time.
func (s *Server[Deps, Output]) Serve(ctx context.Context, transport mcp.Transport) error {
	for {
		message, err := transport.Read(ctx)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		response, ok := s.Handle(ctx, message)
		if !ok {
			continue
		}
		if err := transport.Send(ctx, response); err != nil {
			return err
		}
	}
}

// Handle decodes and answers one JSON-RPC message. A notification
// returns ok=false. The returned bytes are a complete JSON-RPC response.
func (s *Server[Deps, Output]) Handle(ctx context.Context, payload []byte) ([]byte, bool) {
	var message rpcMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		return mustJSON(rpcMessage{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: "parse error"}}), true
	}
	response, ok := s.handle(ctx, message)
	if !ok {
		return nil, false
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return nil, false
	}
	return encoded, true
}

// ServeHTTP handles one MCP streamable-HTTP POST. It supports the
// stateless request shape: the JSON response carries the result, and a
// notification returns 202. Server-initiated SSE streams are not
// implemented; a GET returns 405.
func (s *Server[Deps, Output]) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBody))
	if err != nil {
		http.Error(w, "read request body", http.StatusBadRequest)
		return
	}
	response, ok := s.Handle(r.Context(), body)
	if !ok {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(response)
}

func (s *Server[Deps, Output]) handle(ctx context.Context, message rpcMessage) (rpcMessage, bool) {
	notification := len(message.ID) == 0
	if message.JSONRPC != "" && message.JSONRPC != "2.0" {
		return rpcMessage{JSONRPC: "2.0", ID: message.ID, Error: &rpcError{Code: -32600, Message: "invalid request"}}, !notification
	}
	if notification {
		// Client notifications (initialized, cancelled) need no reply.
		return rpcMessage{}, false
	}
	reply := rpcMessage{JSONRPC: "2.0", ID: message.ID}
	switch message.Method {
	case "initialize":
		reply.Result = mustRaw(map[string]any{
			"protocolVersion": negotiatedVersion(message.Params),
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": s.cfg.Name, "version": s.cfg.Version},
		})
	case "ping":
		reply.Result = json.RawMessage(`{}`)
	case "tools/list":
		reply.Result = mustRaw(map[string]any{"tools": []any{map[string]any{
			"name":        s.cfg.Name,
			"description": s.cfg.Description,
			"inputSchema": s.cfg.InputSchema,
		}}})
	case "tools/call":
		result, err := s.callTool(ctx, message.Params)
		if err != nil {
			reply.Error = &rpcError{Code: -32602, Message: err.Error()}
			break
		}
		reply.Result = result
	case "resources/list":
		reply.Result = mustRaw(map[string]any{"resources": []any{}})
	case "prompts/list":
		reply.Result = mustRaw(map[string]any{"prompts": []any{}})
	default:
		reply.Error = &rpcError{Code: -32601, Message: "method not found: " + message.Method}
	}
	return reply, true
}

func (s *Server[Deps, Output]) callTool(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return nil, fmt.Errorf("invalid tools/call params: %w", err)
	}
	if call.Name != s.cfg.Name {
		return nil, fmt.Errorf("unknown tool %q", call.Name)
	}
	prompt, err := s.prompt(call.Arguments)
	if err != nil {
		return nil, err
	}
	deps, err := s.dependencies(ctx)
	if err != nil {
		return toolError(err.Error()), nil
	}
	result, err := s.agent.Run(ctx, golem.RunContext[Deps]{Deps: deps}, prompt)
	if err != nil {
		return toolError(err.Error()), nil
	}
	text, err := s.format(result.Output)
	if err != nil {
		return toolError("format agent output: " + err.Error()), nil
	}
	return toolText(text, false), nil
}

func (s *Server[Deps, Output]) prompt(args json.RawMessage) (string, error) {
	if s.cfg.Prompt != nil {
		prompt, err := s.cfg.Prompt(args)
		if err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
		if strings.TrimSpace(prompt) == "" {
			return "", fmt.Errorf("prompt is required")
		}
		return prompt, nil
	}
	var input struct {
		Prompt string `json:"prompt"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &input); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
	}
	if strings.TrimSpace(input.Prompt) == "" {
		return "", fmt.Errorf("prompt is required")
	}
	return input.Prompt, nil
}

func (s *Server[Deps, Output]) dependencies(ctx context.Context) (Deps, error) {
	var zero Deps
	if s.cfg.Deps == nil {
		return zero, nil
	}
	return s.cfg.Deps(ctx)
}

func (s *Server[Deps, Output]) format(output Output) (string, error) {
	if s.cfg.Format != nil {
		return s.cfg.Format(output)
	}
	if text, ok := any(output).(string); ok {
		return text, nil
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func negotiatedVersion(params json.RawMessage) string {
	var input struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(params, &input)
	switch input.ProtocolVersion {
	case LegacyProtocolVersion, ModernProtocolVersion:
		return input.ProtocolVersion
	default:
		return LegacyProtocolVersion
	}
}

func toolText(text string, isError bool) json.RawMessage {
	return mustRaw(map[string]any{
		"content": []any{map[string]any{"type": "text", "text": text}},
		"isError": isError,
	})
}

func toolError(message string) json.RawMessage {
	return toolText(message, true)
}

func mustRaw(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

func mustJSON(value rpcMessage) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

var defaultSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"prompt": {"type": "string", "description": "The task or question to send to the agent."}
	},
	"required": ["prompt"],
	"additionalProperties": false
}`)
