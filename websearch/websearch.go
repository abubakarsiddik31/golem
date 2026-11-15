// Package websearch provides a common tool that searches the web and
// returns ranked results — title, URL, and snippet — for the model to
// read, usually followed by webfetch on a chosen URL.
//
// The zero Config uses DuckDuckGo's HTML endpoint, which needs no API
// key. Applications with a different provider implement Backend and pass
// it in; the tool never reads credentials or configuration from the
// environment.
//
// The tool is an ordinary tool.Tool[Deps]: it composes with any agent
// dependency type and every agent option. Its name, description, and
// argument schema are public contract — models and prompts depend on
// them staying stable.
package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/abubakarsiddik31/golem/tool"
)

// ToolName is the name of the tool New returns.
const ToolName = "web_search"

// ToolDescription is the description the model sees for the tool.
const ToolDescription = "Search the web and return ranked results with a title, URL, and snippet. Use web_fetch to read a result's page."

const (
	// DefaultMaxResults caps one search when Config.MaxResults is
	// zero.
	DefaultMaxResults = 5
	// MaxResultsLimit is the largest result count a caller may
	// configure.
	MaxResultsLimit = 20
	// DefaultTimeout bounds one search when Config.HTTPClient is nil
	// and Config.Timeout is zero.
	DefaultTimeout = 15 * time.Second
	// DefaultUserAgent identifies the searcher when Config.UserAgent is
	// empty.
	DefaultUserAgent = "Mozilla/5.0 (compatible; golem-websearch/1.0; +https://github.com/abubakarsiddik31/golem)"
	// maxBodyBytes bounds the HTML a backend response may carry.
	maxBodyBytes int64 = 2 << 20 // 2 MiB
)

// schema describes the tool's arguments; it is public contract.
var schema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"query": {
			"type": "string",
			"description": "Search query."
		},
		"limit": {
			"type": "integer",
			"minimum": 1,
			"maximum": 20,
			"description": "Maximum results to return; omit for the tool's default."
		}
	},
	"required": ["query"],
	"additionalProperties": false
}`)

// Result is one ranked search hit.
type Result struct {
	// Title is the page title the search engine returned.
	Title string
	// URL is the destination page.
	URL string
	// Snippet is the engine's excerpt, possibly empty.
	Snippet string
}

// Backend performs one search. Implementations must honor ctx and
// return at most limit results; the tool also truncates the slice.
type Backend interface {
	Search(ctx context.Context, query string, limit int) ([]Result, error)
}

// Config configures the search tool. The zero value is ready: it uses
// DuckDuckGo with DefaultTimeout and DefaultMaxResults, and reads no
// environment.
type Config struct {
	// Backend performs searches. Nil selects DuckDuckGo, configured by
	// HTTPClient, Timeout, and UserAgent.
	Backend Backend
	// MaxResults caps a search and the model's requested limit; zero
	// selects DefaultMaxResults. Values above MaxResultsLimit fail New.
	MaxResults int
	// HTTPClient performs DuckDuckGo requests; defaults to a client
	// with DefaultTimeout. Cancellation always flows through ctx.
	HTTPClient *http.Client
	// Timeout bounds one DuckDuckGo search with a context deadline when
	// non-zero, whatever client is in use. Zero leaves the deadline to
	// the caller's context and the agent's tool timeout. Negative values
	// fail New.
	Timeout time.Duration
	// UserAgent identifies the searcher in DuckDuckGo requests; empty
	// selects DefaultUserAgent.
	UserAgent string
}

// StatusError reports a completed DuckDuckGo request whose HTTP status
// was outside 2xx.
type StatusError struct {
	// StatusCode is the HTTP status the server returned.
	StatusCode int
	// URL is the requested search URL.
	URL string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("websearch: GET %s: status %d %s", e.URL, e.StatusCode, http.StatusText(e.StatusCode))
}

// searcher holds the resolved configuration behind the tool's exec.
type searcher struct {
	backend    Backend
	maxResults int
}

// New validates cfg and returns the web_search tool ready for
// registration with an agent. Deps is the agent's dependency type; the
// search itself does not use it, but the tool carries it so one
// constructor serves every agent.
func New[Deps any](cfg Config) (tool.Tool[Deps], error) {
	if cfg.Timeout < 0 {
		return tool.Tool[Deps]{}, fmt.Errorf("websearch: timeout must not be negative, got %s", cfg.Timeout)
	}
	if cfg.MaxResults < 0 {
		return tool.Tool[Deps]{}, fmt.Errorf("websearch: max results must not be negative, got %d", cfg.MaxResults)
	}
	if cfg.MaxResults > MaxResultsLimit {
		return tool.Tool[Deps]{}, fmt.Errorf("websearch: max results must be at most %d, got %d", MaxResultsLimit, cfg.MaxResults)
	}
	maxResults := cfg.MaxResults
	if maxResults == 0 {
		maxResults = DefaultMaxResults
	}
	backend := cfg.Backend
	if backend == nil {
		backend = &DuckDuckGo{
			Client:    cfg.HTTPClient,
			Timeout:   cfg.Timeout,
			UserAgent: cfg.UserAgent,
		}
	}
	s := &searcher{backend: backend, maxResults: maxResults}
	return tool.New(tool.Tool[Deps]{
		Name:        ToolName,
		Description: ToolDescription,
		Schema:      schema,
		Exec: func(ctx context.Context, _ Deps, args json.RawMessage) (tool.Result, error) {
			text, err := s.search(ctx, args)
			return tool.Text(text), err
		},
	})
}

// MustNew is New for tools declared as package-level values; it panics
// on invalid configuration.
func MustNew[Deps any](cfg Config) tool.Tool[Deps] {
	t, err := New[Deps](cfg)
	if err != nil {
		panic(err)
	}
	return t
}

func (s *searcher) search(ctx context.Context, args json.RawMessage) (string, error) {
	var input struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &input); err != nil {
			return "", &tool.Failed{Reason: fmt.Sprintf("invalid arguments: %v", err)}
		}
	}
	query := strings.TrimSpace(input.Query)
	if query == "" {
		return "", &tool.Failed{Reason: "query is required"}
	}
	if input.Limit < 0 {
		return "", &tool.Failed{Reason: "limit must not be negative"}
	}
	limit := input.Limit
	if limit == 0 || limit > s.maxResults {
		limit = s.maxResults
	}
	results, err := s.backend.Search(ctx, query, limit)
	if err != nil {
		return "", fmt.Errorf("websearch: search: %w", err)
	}
	if len(results) > limit {
		results = results[:limit]
	}
	if len(results) == 0 {
		return "No results.", nil
	}
	return format(results), nil
}

func format(results []Result) string {
	var b strings.Builder
	for i, result := range results {
		if i > 0 {
			b.WriteString("\n\n")
		}
		title := oneLine(result.Title)
		if title == "" {
			title = result.URL
		}
		fmt.Fprintf(&b, "%d. %s\n%s", i+1, title, result.URL)
		if snippet := oneLine(result.Snippet); snippet != "" {
			fmt.Fprintf(&b, "\n%s", snippet)
		}
	}
	return b.String()
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// DuckDuckGo is the keyless Backend built on DuckDuckGo's HTML endpoint.
// It parses result titles, URLs, and snippets from the markup; the
// endpoint can be overridden for a proxy or an offline test server.
type DuckDuckGo struct {
	// Client performs requests; a client with DefaultTimeout is used
	// when nil.
	Client *http.Client
	// Timeout bounds one request with a context deadline when non-zero.
	Timeout time.Duration
	// UserAgent identifies the client; empty selects DefaultUserAgent.
	UserAgent string
	// Endpoint overrides the HTML search URL; empty selects
	// https://html.duckduckgo.com/html/.
	Endpoint string
}

// Search implements Backend.
func (d *DuckDuckGo) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	endpoint := d.Endpoint
	if endpoint == "" {
		endpoint = "https://html.duckduckgo.com/html/"
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("websearch: endpoint %q: %w", endpoint, err)
	}
	values := parsed.Query()
	values.Set("q", query)
	parsed.RawQuery = values.Encode()

	client := d.Client
	if client == nil {
		client = &http.Client{Timeout: DefaultTimeout}
	}
	if d.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d.Timeout)
		defer cancel()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("websearch: build request: %w", err)
	}
	request.Header.Set("User-Agent", userAgent(d.UserAgent))
	request.Header.Set("Accept", "text/html")

	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, &StatusError{StatusCode: response.StatusCode, URL: parsed.String()}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("websearch: read response: %w", err)
	}
	return parseResults(string(body), limit), nil
}

func userAgent(configured string) string {
	if configured != "" {
		return configured
	}
	return DefaultUserAgent
}

var (
	openTagRE    = regexp.MustCompile(`(?is)<([a-zA-Z][a-zA-Z0-9]*)((?:\s+[^<>]*?)?)\s*/?>`)
	attrRE       = regexp.MustCompile(`(?is)([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*=\s*("([^"]*)"|'([^']*)'|([^\s"'>]+))`)
	tagStrip     = regexp.MustCompile(`(?s)<[^>]*>`)
	punctSpaceRE = regexp.MustCompile(`\s+([.,;:!?])`)
)

// parseResults extracts ordered results from DuckDuckGo HTML. It
// tolerates the html and lite endpoints' class names and attribute order,
// skips sponsored blocks, unwraps redirect links, and pairs each snippet
// with the result anchor it follows.
func parseResults(body string, limit int) []Result {
	var results []Result
	seen := make(map[string]struct{})
	inAd := false
	current := -1
	for _, match := range openTagRE.FindAllStringSubmatchIndex(body, -1) {
		tag := strings.ToLower(body[match[2]:match[3]])
		attrs := parseAttrs(body[match[4]:match[5]])
		class := attrs["class"]
		lowerClass := strings.ToLower(class)
		if tag != "a" && !hasClass(class, "result__snippet", "result-snippet") && strings.Contains(lowerClass, "result") {
			inAd = hasSubstring(class, "sponsored", "result--ad")
			current = -1
		}
		if hasSubstring(class, "sponsored", "result--ad") {
			inAd = true
			current = -1
			continue
		}
		contentEnd := closingTag(body, match[1], tag)
		if contentEnd < 0 {
			continue
		}
		switch {
		case tag == "a" && hasClass(class, "result__a", "result-link"):
			if inAd {
				continue
			}
			href := attrs["href"]
			if href == "" {
				continue
			}
			target := unwrapURL(href)
			if target == "" {
				continue
			}
			if _, duplicate := seen[target]; duplicate {
				current = -1
				continue
			}
			seen[target] = struct{}{}
			results = append(results, Result{
				Title: cleanText(body[match[1]:contentEnd]),
				URL:   target,
			})
			current = len(results) - 1
			inAd = false
			if len(results) == limit {
				return results
			}
		case hasClass(class, "result__snippet", "result-snippet"):
			if inAd || current < 0 || results[current].Snippet != "" {
				continue
			}
			results[current].Snippet = cleanText(body[match[1]:contentEnd])
		}
	}
	return results
}

func parseAttrs(raw string) map[string]string {
	attrs := make(map[string]string)
	for _, match := range attrRE.FindAllStringSubmatch(raw, -1) {
		name := strings.ToLower(match[1])
		value := match[3]
		if value == "" {
			value = match[4]
		}
		if value == "" && match[5] != "" {
			value = match[5]
		}
		attrs[name] = value
	}
	return attrs
}

func hasClass(class string, names ...string) bool {
	for _, field := range strings.Fields(class) {
		for _, name := range names {
			if strings.EqualFold(field, name) {
				return true
			}
		}
	}
	return false
}

func hasSubstring(class string, needles ...string) bool {
	lower := strings.ToLower(class)
	for _, needle := range needles {
		if strings.Contains(lower, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

func closingTag(body string, from int, tag string) int {
	needle := "</" + strings.ToLower(tag)
	lower := strings.ToLower(body[from:])
	index := strings.Index(lower, needle)
	if index < 0 {
		return -1
	}
	return from + index
}

func cleanText(raw string) string {
	text := oneLine(html.UnescapeString(tagStrip.ReplaceAllString(raw, " ")))
	return punctSpaceRE.ReplaceAllString(text, "$1")
}

func unwrapURL(raw string) string {
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if strings.Contains(strings.ToLower(parsed.Host), "duckduckgo.com") && parsed.Path == "/l/" {
		if target := parsed.Query().Get("uddg"); target != "" {
			return target
		}
	}
	return raw
}
