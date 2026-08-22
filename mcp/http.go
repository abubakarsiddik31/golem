package mcp

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// HTTPConfig configures a streamable-HTTP transport: every message is
// one POST to the endpoint, and responses arrive either as a JSON body
// or as events of a text/event-stream response.
type HTTPConfig struct {
	// URL is the server's MCP endpoint; required, http or https.
	URL string
	// HTTPClient performs requests; defaults to a client with no
	// built-in timeout so event streams are bounded only by the
	// caller's context. Callers wanting a wall-clock bound supply
	// their own.
	HTTPClient *http.Client
	// Header carries extra headers sent on every request, such as
	// Authorization. It is copied at construction.
	Header http.Header
}

// HTTPTransport speaks MCP over the streamable-HTTP transport. It
// implements Transport and, like the client that drives it, is used
// one request at a time: Send performs the POST and stages whatever
// the response carries; Read returns staged messages, then blocks
// until the context is done. The session header a server assigns at
// initialize is captured and sent on every later request.
type HTTPTransport struct {
	endpoint   *url.URL
	client     *http.Client
	header     http.Header
	sessionID  string
	pending    [][]byte
	body       io.ReadCloser
	bodyReader *bufio.Reader
}

var _ Transport = (*HTTPTransport)(nil)

// NewHTTP validates cfg and returns a transport ready for
// mcp.NewClient.
func NewHTTP(cfg HTTPConfig) (*HTTPTransport, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("mcp: url is required")
	}
	endpoint, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("mcp: url is not valid: %w", err)
	}
	if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
		return nil, fmt.Errorf("mcp: url must be http or https, got %q", cfg.URL)
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	header := http.Header{}
	for name, values := range cfg.Header {
		header[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
	}
	return &HTTPTransport{
		endpoint: endpoint,
		client:   client,
		header:   header,
	}, nil
}

// HTTPStatusError reports a non-2xx response from the server,
// including the 404 a server uses when its session no longer exists.
type HTTPStatusError struct {
	// StatusCode is the HTTP status the server returned.
	StatusCode int
	// URL is the endpoint that was called.
	URL string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("mcp: POST %s: status %d %s", e.URL, e.StatusCode, http.StatusText(e.StatusCode))
}

// Send POSTs one message and stages the response: a JSON body becomes
// the next message Read returns, an event stream becomes a sequence of
// them, and a bodyless 2xx (a notification's Accepted) stages nothing.
func (t *HTTPTransport) Send(ctx context.Context, message []byte) error {
	t.discardBody()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint.String(), bytes.NewReader(message))
	if err != nil {
		return fmt.Errorf("mcp: build request: %w", err)
	}
	request.Header = t.header.Clone()
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	if t.sessionID != "" {
		request.Header.Set("Mcp-Session-Id", t.sessionID)
	}
	response, err := t.client.Do(request)
	if err != nil {
		return fmt.Errorf("mcp: POST %s: %w", t.endpoint.String(), err)
	}
	if session := response.Header.Get("Mcp-Session-Id"); session != "" {
		t.sessionID = session
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		response.Body.Close()
		return &HTTPStatusError{StatusCode: response.StatusCode, URL: t.endpoint.String()}
	}

	mediaType := mediaType(response.Header.Get("Content-Type"))
	switch {
	case mediaType == "application/json":
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			return fmt.Errorf("mcp: read response: %w", err)
		}
		if len(bytes.TrimSpace(body)) > 0 {
			t.pending = append(t.pending, body)
		}
	case mediaType == "text/event-stream":
		t.body = response.Body
		t.bodyReader = bufio.NewReaderSize(response.Body, 64*1024)
	default:
		// A bodyless 2xx (202 Accepted for notifications, or an empty
		// 200): nothing to read.
		response.Body.Close()
	}
	return nil
}

// Read returns the next staged message — a JSON response body, or the
// next event of an open stream — and otherwise blocks until the
// context is done. A stream that ends without a response therefore
// surfaces here as the context's error once it expires.
func (t *HTTPTransport) Read(ctx context.Context) ([]byte, error) {
	if len(t.pending) > 0 {
		next := t.pending[0]
		t.pending = t.pending[1:]
		return next, nil
	}
	if t.body != nil {
		event, err := t.readEvent()
		if event != nil {
			return event, nil
		}
		t.discardBody()
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("mcp: read event stream: %w", err)
		}
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

// Close releases any open response body.
func (t *HTTPTransport) Close() error {
	t.discardBody()
	return nil
}

// discardBody drops an unfinished event-stream body.
func (t *HTTPTransport) discardBody() {
	if t.body != nil {
		t.body.Close()
		t.body = nil
		t.bodyReader = nil
	}
}

// readEvent returns the data of the next server-sent event, or nil at
// end of stream. Comments, keep-alives, and non-data fields are
// skipped; multi-line data joins with newlines per SSE.
func (t *HTTPTransport) readEvent() ([]byte, error) {
	var data []string
	for {
		line, err := t.bodyReader.ReadString('\n')
		trimmed := strings.TrimRight(line, "\r\n")
		switch {
		case trimmed == "" && err == nil:
			if len(data) > 0 {
				return []byte(strings.Join(data, "\n")), nil
			}
		case strings.HasPrefix(trimmed, ":"):
			// Comment or keep-alive.
		case strings.HasPrefix(trimmed, "data:"):
			data = append(data, strings.TrimSpace(strings.TrimPrefix(trimmed, "data:")))
		default:
			// event:, id:, retry: — ignored.
		}
		if err != nil {
			if err == io.EOF && len(data) > 0 {
				return []byte(strings.Join(data, "\n")), nil
			}
			return nil, err
		}
	}
}

// mediaType extracts the lowercase media type from a Content-Type
// header value, dropping parameters.
func mediaType(contentType string) string {
	return strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
}
