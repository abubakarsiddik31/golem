package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// tokenServer answers every request with an empty JSON-RPC result and
// records the Authorization header it saw.
type tokenServer struct {
	url           string
	authorization []string
	status        int
	challenge     string
}

func (s *tokenServer) handler(w http.ResponseWriter, r *http.Request) {
	s.authorization = append(s.authorization, r.Header.Get("Authorization"))
	if s.status != 0 {
		if s.challenge != "" {
			w.Header().Set("WWW-Authenticate", s.challenge)
		}
		http.Error(w, "denied", s.status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
}

func (s *tokenServer) transport(t *testing.T, cfg HTTPConfig) *HTTPTransport {
	t.Helper()
	httpServer := httptest.NewServer(http.HandlerFunc(s.handler))
	t.Cleanup(httpServer.Close)
	s.url = httpServer.URL
	cfg.URL = httpServer.URL
	transport, err := NewHTTP(cfg)
	if err != nil {
		t.Fatalf("NewHTTP error = %v", err)
	}
	t.Cleanup(func() { transport.Close() })
	return transport
}

// listRequest is a message with no modern metadata, so the transport
// sends it as a legacy request.
var listRequest = []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)

func TestTokenSourceSendsBearerOnEveryRequest(t *testing.T) {
	t.Parallel()

	server := &tokenServer{}
	issued := 0
	transport := server.transport(t, HTTPConfig{
		TokenSource: func(context.Context) (string, error) {
			issued++
			return fmt.Sprintf("token-%d", issued), nil
		},
	})

	for i := 0; i < 2; i++ {
		if err := transport.Send(context.Background(), listRequest); err != nil {
			t.Fatalf("Send error = %v", err)
		}
	}
	if len(server.authorization) != 2 {
		t.Fatalf("server saw %d requests, want 2", len(server.authorization))
	}
	if want := "Bearer token-1"; server.authorization[0] != want {
		t.Errorf("first Authorization = %q, want %q", server.authorization[0], want)
	}
	// The token source runs per request, so a refreshed token is used.
	if want := "Bearer token-2"; server.authorization[1] != want {
		t.Errorf("second Authorization = %q, want %q", server.authorization[1], want)
	}
}

func TestTokenSourceReplacesStaticAuthorizationHeader(t *testing.T) {
	t.Parallel()

	server := &tokenServer{}
	header := http.Header{}
	header.Set("Authorization", "Basic c3RhdGlj")
	header.Set("X-Trace", "keep")
	transport := server.transport(t, HTTPConfig{
		Header: header,
		TokenSource: func(context.Context) (string, error) {
			return "dynamic", nil
		},
	})

	if err := transport.Send(context.Background(), listRequest); err != nil {
		t.Fatalf("Send error = %v", err)
	}
	if want := "Bearer dynamic"; server.authorization[0] != want {
		t.Errorf("Authorization = %q, want %q", server.authorization[0], want)
	}
}

func TestTokenSourceEmptyTokenSendsNoAuthorization(t *testing.T) {
	t.Parallel()

	server := &tokenServer{}
	header := http.Header{}
	header.Set("Authorization", "Basic c3RhdGlj")
	transport := server.transport(t, HTTPConfig{
		Header: header,
		TokenSource: func(context.Context) (string, error) {
			return "", nil
		},
	})

	if err := transport.Send(context.Background(), listRequest); err != nil {
		t.Fatalf("Send error = %v", err)
	}
	if got := server.authorization[0]; got != "" {
		t.Errorf("Authorization = %q, want none", got)
	}
}

func TestTokenSourceErrorFailsTheRequest(t *testing.T) {
	t.Parallel()

	server := &tokenServer{}
	transport := server.transport(t, HTTPConfig{
		TokenSource: func(context.Context) (string, error) {
			return "", errors.New("refresh failed")
		},
	})

	err := transport.Send(context.Background(), listRequest)
	if err == nil || !strings.Contains(err.Error(), "refresh failed") {
		t.Fatalf("Send error = %v, want the token source's error", err)
	}
	if len(server.authorization) != 0 {
		t.Error("request reached the server despite a token source error")
	}
}

func TestTokenSourceRejectsUnsafeToken(t *testing.T) {
	t.Parallel()

	server := &tokenServer{}
	transport := server.transport(t, HTTPConfig{
		TokenSource: func(context.Context) (string, error) {
			return "good\r\nX-Injected: yes", nil
		},
	})

	err := transport.Send(context.Background(), listRequest)
	if err == nil || !strings.Contains(err.Error(), "header-safe") {
		t.Fatalf("Send error = %v, want a header-safety failure", err)
	}
	if len(server.authorization) != 0 {
		t.Error("a token that could inject a header reached the server")
	}
}

func TestUnauthorizedReturnsAuthError(t *testing.T) {
	t.Parallel()

	const challenge = `Bearer realm="mcp", error="invalid_token", error_description="The access token expired", resource_metadata="https://example.com/.well-known/oauth-protected-resource"`
	server := &tokenServer{status: http.StatusUnauthorized, challenge: challenge}
	transport := server.transport(t, HTTPConfig{
		TokenSource: func(context.Context) (string, error) {
			return "stale", nil
		},
	})

	err := transport.Send(context.Background(), listRequest)
	var unauthorized *AuthError
	if !errors.As(err, &unauthorized) {
		t.Fatalf("Send error = %v, want *AuthError", err)
	}
	if unauthorized.Status == nil || unauthorized.Status.StatusCode != http.StatusUnauthorized {
		t.Errorf("AuthError.Status = %+v, want a 401", unauthorized.Status)
	}
	if unauthorized.Header != challenge {
		t.Errorf("AuthError.Header = %q, want the raw header", unauthorized.Header)
	}
	want := Challenge{
		Scheme:           "Bearer",
		Realm:            "mcp",
		Error:            "invalid_token",
		Description:      "The access token expired",
		ResourceMetadata: "https://example.com/.well-known/oauth-protected-resource",
	}
	if unauthorized.Challenge != want {
		t.Errorf("AuthError.Challenge = %+v, want %+v", unauthorized.Challenge, want)
	}
	if !strings.Contains(err.Error(), "invalid_token") {
		t.Errorf("Error() = %q, want the challenge's error code", err.Error())
	}
}

func TestForbiddenWithoutChallengeReturnsAuthError(t *testing.T) {
	t.Parallel()

	server := &tokenServer{status: http.StatusForbidden}
	transport := server.transport(t, HTTPConfig{})

	err := transport.Send(context.Background(), listRequest)
	var forbidden *AuthError
	if !errors.As(err, &forbidden) {
		t.Fatalf("Send error = %v, want *AuthError", err)
	}
	if forbidden.Challenge != (Challenge{}) {
		t.Errorf("AuthError.Challenge = %+v, want the zero challenge", forbidden.Challenge)
	}
	if want := "mcp: POST " + server.url + ": status 403 Forbidden"; err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
}

func TestUnauthorizedJoinsEveryChallengeHeader(t *testing.T) {
	t.Parallel()

	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("WWW-Authenticate", `Basic realm="legacy"`)
		w.Header().Add("WWW-Authenticate", `Bearer error="insufficient_scope", scope="tools:write"`)
		http.Error(w, "denied", http.StatusForbidden)
	}))
	defer httpServer.Close()

	transport, err := NewHTTP(HTTPConfig{URL: httpServer.URL})
	if err != nil {
		t.Fatalf("NewHTTP error = %v", err)
	}
	err = transport.Send(context.Background(), listRequest)
	var forbidden *AuthError
	if !errors.As(err, &forbidden) {
		t.Fatalf("Send error = %v, want *AuthError", err)
	}
	if want := `Basic realm="legacy", Bearer error="insufficient_scope", scope="tools:write"`; forbidden.Header != want {
		t.Errorf("AuthError.Header = %q, want %q", forbidden.Header, want)
	}
	if forbidden.Challenge.Scope != "tools:write" {
		t.Errorf("Challenge.Scope = %q, want tools:write", forbidden.Challenge.Scope)
	}
}

func TestAuthErrorUnwrapsToStatusError(t *testing.T) {
	t.Parallel()

	server := &tokenServer{status: http.StatusUnauthorized, challenge: `Bearer error="invalid_token"`}
	transport := server.transport(t, HTTPConfig{})

	err := transport.Send(context.Background(), listRequest)
	var status *HTTPStatusError
	if !errors.As(err, &status) {
		t.Fatalf("Send error = %v, want it to unwrap to *HTTPStatusError", err)
	}
	if status.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, want 401", status.StatusCode)
	}
}

func TestOtherStatusesStayHTTPStatusError(t *testing.T) {
	t.Parallel()

	server := &tokenServer{status: http.StatusInternalServerError}
	transport := server.transport(t, HTTPConfig{})

	err := transport.Send(context.Background(), listRequest)
	var unauthorized *AuthError
	if errors.As(err, &unauthorized) {
		t.Fatalf("Send error = %v, want a plain *HTTPStatusError", err)
	}
	var status *HTTPStatusError
	if !errors.As(err, &status) {
		t.Fatalf("Send error = %v, want *HTTPStatusError", err)
	}
}

func TestParseBearerChallenge(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		header string
		want   Challenge
	}{
		{
			name:   "empty",
			header: "",
			want:   Challenge{},
		},
		{
			name:   "scheme only",
			header: "Bearer",
			want:   Challenge{Scheme: "Bearer"},
		},
		{
			name:   "realm",
			header: `Bearer realm="mcp"`,
			want:   Challenge{Scheme: "Bearer", Realm: "mcp"},
		},
		{
			name:   "case insensitive scheme",
			header: `bearer error="invalid_token"`,
			want:   Challenge{Scheme: "bearer", Error: "invalid_token"},
		},
		{
			name:   "insufficient scope",
			header: `Bearer error="insufficient_scope", scope="tools:write tools:read"`,
			want: Challenge{
				Scheme: "Bearer",
				Error:  "insufficient_scope",
				Scope:  "tools:write tools:read",
			},
		},
		{
			name:   "second challenge",
			header: `Basic realm="legacy", Bearer error="invalid_token"`,
			want:   Challenge{Scheme: "Bearer", Error: "invalid_token"},
		},
		{
			name:   "comma inside a quoted value",
			header: `Bearer error_description="expired, please renew"`,
			want: Challenge{
				Scheme:      "Bearer",
				Description: "expired, please renew",
			},
		},
		{
			name:   "escaped quote",
			header: `Bearer realm="a\"b"`,
			want:   Challenge{Scheme: "Bearer", Realm: `a"b`},
		},
		{
			name:   "unquoted value",
			header: `Bearer error=invalid_token`,
			want:   Challenge{Scheme: "Bearer", Error: "invalid_token"},
		},
		{
			name:   "unknown parameters ignored",
			header: `Bearer error="invalid_token", other="x"`,
			want:   Challenge{Scheme: "Bearer", Error: "invalid_token"},
		},
		{
			name:   "no bearer challenge",
			header: `Basic realm="legacy"`,
			want:   Challenge{},
		},
		{
			name:   "bearer after a token68 challenge",
			header: `Basic dXNlcjpwYXNz, Bearer scope="a b"`,
			want:   Challenge{Scheme: "Bearer", Scope: "a b"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := parseBearerChallenge(test.header); got != test.want {
				t.Errorf("parseBearerChallenge(%q) = %+v, want %+v", test.header, got, test.want)
			}
		})
	}
}

func TestInitializeSurfacesAuthErrorWithoutFallback(t *testing.T) {
	t.Parallel()

	const challenge = `Bearer error="invalid_token"`
	var requests int
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("WWW-Authenticate", challenge)
		http.Error(w, "denied", http.StatusUnauthorized)
	}))
	defer httpServer.Close()

	transport, err := NewHTTP(HTTPConfig{URL: httpServer.URL})
	if err != nil {
		t.Fatalf("NewHTTP error = %v", err)
	}
	client := NewClient(transport)
	err = client.Initialize(context.Background())
	var unauthorized *AuthError
	if !errors.As(err, &unauthorized) {
		t.Fatalf("Initialize error = %v, want *AuthError", err)
	}
	if unauthorized.Challenge.Error != "invalid_token" {
		t.Errorf("Challenge.Error = %q, want invalid_token", unauthorized.Challenge.Error)
	}
	// The legacy handshake would be refused too, so it must not be tried.
	if requests != 1 {
		t.Errorf("server saw %d requests, want only the discovery probe", requests)
	}
}

// Ensure the exported type is safe to construct directly, without a
// wrapped status.
func TestAuthErrorWithoutStatus(t *testing.T) {
	t.Parallel()

	err := &AuthError{Challenge: Challenge{Error: "insufficient_scope"}}
	if want := "mcp: request was not authorized: insufficient_scope"; err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
}
