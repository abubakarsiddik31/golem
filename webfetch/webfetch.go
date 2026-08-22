// Package webfetch provides Golem's first common tool: fetch an http or
// https URL with GET and return the response body as text a model can
// read. HTML is reduced to its visible text; other text-like formats
// pass through unchanged.
//
// The tool is an ordinary tool.Tool[Deps]: it composes with any agent
// dependency type and every agent option. Its name, description, and
// argument schema are public contract — models and prompts depend on
// them staying stable.
package webfetch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

// ToolName is the name of the tool New returns.
const ToolName = "web_fetch"

// ToolDescription is the description the model sees for the tool.
const ToolDescription = "Fetch an http or https URL with GET and return the response body as text. HTML pages are reduced to their visible text; other text-like formats such as plain text, JSON, XML, YAML, CSV, and Markdown pass through unchanged."

const (
	// DefaultUserAgent identifies the fetcher when Config.UserAgent is
	// empty.
	DefaultUserAgent = "golem-webfetch"
	// DefaultMaxBytes bounds a response body when Config.MaxBytes is zero.
	DefaultMaxBytes int64 = 1 << 20 // 1 MiB
	// DefaultTimeout bounds a fetch when Config.HTTPClient is nil and
	// Config.Timeout is zero.
	DefaultTimeout = 30 * time.Second
)

// schema describes the tool's single url argument; it is public contract.
var schema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"url": {
			"type": "string",
			"description": "Absolute http or https URL to fetch with GET."
		}
	},
	"required": ["url"],
	"additionalProperties": false
}`)

// Config configures the fetch tool. The zero value is ready: it uses a
// client with DefaultTimeout, caps bodies at DefaultMaxBytes, and sends
// DefaultUserAgent. There are no implicit environment reads.
type Config struct {
	// HTTPClient performs requests; defaults to a client with a
	// DefaultTimeout. Cancellation always flows through ctx.
	HTTPClient *http.Client
	// Timeout bounds one fetch with a context deadline when non-zero,
	// whatever client is in use; zero leaves the deadline to the caller's
	// context and the agent's tool timeout. Negative values fail New.
	Timeout time.Duration
	// MaxBytes caps bytes read from the response body; a truncated body
	// is returned with a truncation marker rather than failing. Zero
	// selects DefaultMaxBytes; negative values fail New.
	MaxBytes int64
	// UserAgent identifies the fetcher in requests; empty selects
	// DefaultUserAgent.
	UserAgent string
}

// StatusError reports a completed fetch whose HTTP status was outside
// 2xx. The response body is not read.
type StatusError struct {
	// StatusCode is the HTTP status the server returned.
	StatusCode int
	// URL is the requested URL after any redirects.
	URL string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("webfetch: GET %s: status %d %s", e.URL, e.StatusCode, http.StatusText(e.StatusCode))
}

// UnsupportedContentTypeError reports a 2xx response whose media type
// the tool does not read — HTML and text-like types only, so binary
// formats never reach the model.
type UnsupportedContentTypeError struct {
	// ContentType is the response media type, lowercased, without
	// parameters; empty when the response had no Content-Type.
	ContentType string
	// URL is the requested URL after any redirects.
	URL string
}

func (e *UnsupportedContentTypeError) Error() string {
	if e.ContentType == "" {
		return fmt.Sprintf("webfetch: GET %s: no content type and the body is not text-like", e.URL)
	}
	return fmt.Sprintf("webfetch: GET %s: unsupported content type %q", e.URL, e.ContentType)
}

// fetcher holds the resolved configuration behind the tool's exec.
type fetcher struct {
	client    *http.Client
	timeout   time.Duration
	maxBytes  int64
	userAgent string
}

// New validates cfg and returns the web_fetch tool ready for
// registration with an agent. Deps is the agent's dependency type; the
// fetch itself does not use it, but the tool carries it so one
// constructor serves every agent.
func New[Deps any](cfg Config) (tool.Tool[Deps], error) {
	if cfg.Timeout < 0 {
		return tool.Tool[Deps]{}, fmt.Errorf("webfetch: timeout must not be negative, got %s", cfg.Timeout)
	}
	if cfg.MaxBytes < 0 {
		return tool.Tool[Deps]{}, fmt.Errorf("webfetch: max bytes must not be negative, got %d", cfg.MaxBytes)
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: DefaultTimeout}
	}
	maxBytes := cfg.MaxBytes
	if maxBytes == 0 {
		maxBytes = DefaultMaxBytes
	}
	userAgent := cfg.UserAgent
	if userAgent == "" {
		userAgent = DefaultUserAgent
	}
	f := &fetcher{
		client:    client,
		timeout:   cfg.Timeout,
		maxBytes:  maxBytes,
		userAgent: userAgent,
	}
	return tool.New(tool.Tool[Deps]{
		Name:        ToolName,
		Description: ToolDescription,
		Schema:      schema,
		Exec: func(ctx context.Context, deps Deps, args json.RawMessage) (string, error) {
			return f.fetch(ctx, args)
		},
	})
}

// MustNew is New for tools declared as package-level values; it panics
// on invalid configuration.
func MustNew[Deps any](cfg Config) tool.Tool[Deps] {
	validated, err := New[Deps](cfg)
	if err != nil {
		panic(err)
	}
	return validated
}

// fetch runs one web_fetch call: validate the model-produced URL as
// correctable arguments, GET it, and reduce the response to text.
func (f *fetcher) fetch(ctx context.Context, args json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	target, err := parseArgs(args)
	if err != nil {
		return "", err
	}

	if f.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, f.timeout)
		defer cancel()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", fmt.Errorf("webfetch: GET %s: %w", target, err)
	}
	request.Header.Set("User-Agent", f.userAgent)
	response, err := f.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("webfetch: GET %s: %w", target, err)
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode > 299 {
		return "", &StatusError{StatusCode: response.StatusCode, URL: target}
	}
	mediaType := mediaTypeOf(response.Header.Get("Content-Type"))
	if !isTextMediaType(mediaType) {
		return "", &UnsupportedContentTypeError{ContentType: mediaType, URL: target}
	}

	body, truncated, err := readBody(response.Body, f.maxBytes)
	if err != nil {
		return "", fmt.Errorf("webfetch: GET %s: read response: %w", target, err)
	}

	var text string
	if mediaType == "text/html" || mediaType == "application/xhtml+xml" {
		text = htmlToText(body)
	} else {
		text = body
	}
	if truncated {
		text += fmt.Sprintf("\n\n[webfetch: response truncated at %d bytes]", f.maxBytes)
	}
	return text, nil
}

// parseArgs extracts and validates the url argument. Malformed or
// non-http arguments are model mistakes, so they reject as correctable
// *model.ModelRetry rather than failing the run.
func parseArgs(args json.RawMessage) (string, error) {
	var input struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return "", &model.ModelRetry{Err: fmt.Errorf("arguments must be an object with a string url: %w", err)}
	}
	if input.URL == "" {
		return "", &model.ModelRetry{Err: fmt.Errorf("url is required")}
	}
	parsed, err := url.Parse(input.URL)
	if err != nil {
		return "", &model.ModelRetry{Err: fmt.Errorf("url is not a valid URL: %w", err)}
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", &model.ModelRetry{Err: fmt.Errorf("url must be an absolute http or https URL, got %q", input.URL)}
	}
	return input.URL, nil
}

// mediaTypeOf extracts the lowercase media type from a Content-Type
// header value; a missing or malformed header yields "".
func mediaTypeOf(contentType string) string {
	if contentType == "" {
		return ""
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	}
	return mediaType
}

// passthroughMediaTypes are the non-HTML types whose bodies are
// returned unchanged. Any text/* type passes through too.
var passthroughMediaTypes = map[string]bool{
	"application/json":      true,
	"application/xml":       true,
	"application/yaml":      true,
	"application/x-yaml":    true,
	"application/xhtml+xml": true,
}

func isTextMediaType(mediaType string) bool {
	return strings.HasPrefix(mediaType, "text/") || passthroughMediaTypes[mediaType]
}

// readBody reads at most limit bytes and reports whether the body was
// longer. One byte past the limit is read to detect truncation without
// reading unbounded content.
func readBody(body io.Reader, limit int64) (string, bool, error) {
	raw, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return "", false, err
	}
	truncated := int64(len(raw)) > limit
	if truncated {
		raw = raw[:limit]
	}
	return string(raw), truncated, nil
}
