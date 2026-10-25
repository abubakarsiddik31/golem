// Command mcp-auth shows the mcp package's authorization surface: a local
// HTTP MCP server refuses an unauthenticated request with a challenge, the
// client reports it as *mcp.AuthError, and the application re-authorizes
// and retries. The transport itself never obtains a token or retries — it
// only carries what TokenSource supplies and reports what the server asked
// for. Everything here is local, offline, and deterministic.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/abubakarsiddik31/golem/mcp"
)

func main() {
	// Stand-in for a remote MCP endpoint on the loopback interface.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Println("Listen:", err)
		return
	}
	endpoint := "http://" + listener.Addr().String()
	server := &http.Server{Handler: &authServer{
		// RFC 9728: where an application discovers the authorization
		// server from. A real one would fetch this document.
		metadata: endpoint + "/.well-known/oauth-protected-resource",
	}}
	go server.Serve(listener)
	defer server.Close()

	if err := run(endpoint); err != nil {
		fmt.Println(err)
	}
}

func run(endpoint string) error {
	// The token lives in the application. TokenSource is called before
	// every request, so assigning a new one takes effect immediately —
	// no transport rebuild.
	token := ""
	transport, err := mcp.NewHTTP(mcp.HTTPConfig{
		URL: endpoint,
		TokenSource: func(context.Context) (string, error) {
			return token, nil
		},
	})
	if err != nil {
		return fmt.Errorf("NewHTTP: %w", err)
	}
	client := mcp.NewClient(transport)
	defer client.Close()

	ctx := context.Background()
	if err := client.Initialize(ctx); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	fmt.Printf("connected to %s %s (%s)\n\n",
		client.Server().Name, client.Server().Version, client.ProtocolVersion())

	fmt.Println("1. request with no credentials")
	_, err = client.ListTools(ctx)
	challenge, err := challengeOf(err)
	if err != nil {
		return err
	}
	fmt.Printf("   refused: %s\n", challenge.Error)
	fmt.Printf("   protected resource metadata: %s\n", challenge.ResourceMetadata)

	fmt.Println("2. authorize for tools:read")
	token = "scout" // what a real OAuth flow would return
	infos, err := client.ListTools(ctx)
	if err != nil {
		return fmt.Errorf("ListTools: %w", err)
	}
	for _, info := range infos {
		fmt.Printf("   tool %s: %s\n", info.Name, info.Description)
	}

	fmt.Println("3. call a tool that needs tools:write")
	_, err = client.CallTool(ctx, "deploy", json.RawMessage(`{"target":"production"}`))
	challenge, err = challengeOf(err)
	if err != nil {
		return err
	}
	fmt.Printf("   refused: %s, needs %s\n", challenge.Error, challenge.Scope)

	fmt.Println("4. step up to tools:write and retry")
	token = "pilot" // the union of the granted and requested scopes
	result, err := client.CallTool(ctx, "deploy", json.RawMessage(`{"target":"production"}`))
	if err != nil {
		return fmt.Errorf("CallTool: %w", err)
	}
	fmt.Printf("   %s\n", result.Text())
	return nil
}

// challengeOf unwraps an authorization refusal. Any other error is
// returned unchanged, so a real failure is never mistaken for a
// re-authorization prompt.
func challengeOf(err error) (mcp.Challenge, error) {
	var unauthorized *mcp.AuthError
	if !errors.As(err, &unauthorized) {
		return mcp.Challenge{}, fmt.Errorf("expected an authorization refusal, got %v", err)
	}
	return unauthorized.Challenge, nil
}

// authServer is a minimal modern MCP server that requires a bearer token
// on everything except discovery. Its tokens stand in for the result of a
// real OAuth flow.
type authServer struct {
	metadata string
}

// scopesByToken is the demo's authorization server: each token grants a
// fixed scope set.
var scopesByToken = map[string]map[string]bool{
	"scout": {"tools:read": true},
	"pilot": {"tools:read": true, "tools:write": true},
}

func (s *authServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var message struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	// Discovery is unauthenticated so a client can learn what the
	// resource is before it has a token.
	if message.Method == "server/discover" {
		reply(w, message.ID, `{"resultType":"complete","supportedVersions":["2026-07-28"],"capabilities":{"tools":{}},"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"deploy-server","version":"1.0"}}}`)
		return
	}

	scopes, known := scopesByToken[bearer(r)]
	if !known {
		w.Header().Set("WWW-Authenticate",
			fmt.Sprintf(`Bearer error="invalid_token", resource_metadata=%q`, s.metadata))
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	required := ""
	switch message.Method {
	case "tools/list":
		required = "tools:read"
	case "tools/call":
		required = "tools:write"
	}
	if required != "" && !scopes[required] {
		// RFC 6750: name every scope the operation needs, so the
		// client can request them all at once.
		w.Header().Set("WWW-Authenticate",
			fmt.Sprintf(`Bearer error="insufficient_scope", scope=%q`, required))
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	switch message.Method {
	case "tools/list":
		reply(w, message.ID, `{"resultType":"complete","tools":[{"name":"deploy","description":"Deploy the application to an environment.","inputSchema":{"type":"object","properties":{"target":{"type":"string"}},"required":["target"]}}]}`)
	case "tools/call":
		reply(w, message.ID, `{"resultType":"complete","content":[{"type":"text","text":"deployed to production"}]}`)
	default:
		replyError(w, message.ID, -32601, "method not found: "+message.Method)
	}
}

// bearer returns the request's bearer token, or "" when it has none.
func bearer(r *http.Request) string {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return ""
	}
	return token
}

func reply(w http.ResponseWriter, id json.RawMessage, result string) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":%s}`, id, result)
}

func replyError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":%d,"message":%q}}`, id, code, message)
}
