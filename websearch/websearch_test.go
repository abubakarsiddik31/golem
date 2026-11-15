package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem/tool"
)

type fakeBackend struct {
	results []Result
	err     error
	query   string
	limit   int
}

func (f *fakeBackend) Search(_ context.Context, query string, limit int) ([]Result, error) {
	f.query, f.limit = query, limit
	return f.results, f.err
}

func searchTool(t *testing.T, cfg Config) tool.Tool[struct{}] {
	t.Helper()
	search, err := New[struct{}](cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return search
}

func TestToolFormatsResultsAndClampsLimit(t *testing.T) {
	t.Parallel()

	backend := &fakeBackend{results: []Result{
		{Title: "One", URL: "https://example.com/one", Snippet: "First result."},
		{Title: "Two", URL: "https://example.com/two"},
		{Title: "Three", URL: "https://example.com/three"},
	}}
	search := searchTool(t, Config{Backend: backend, MaxResults: 2})

	result, err := search.Exec(context.Background(), struct{}{}, json.RawMessage(`{"query":" go web ","limit":9}`))
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if backend.query != "go web" || backend.limit != 2 {
		t.Fatalf("backend saw query=%q limit=%d, want trimmed query and clamped limit", backend.query, backend.limit)
	}
	if strings.Contains(result.Text, "Three") {
		t.Fatalf("result was not truncated: %q", result.Text)
	}
	want := "1. One\nhttps://example.com/one\nFirst result.\n\n2. Two\nhttps://example.com/two"
	if result.Text != want {
		t.Fatalf("result = %q, want %q", result.Text, want)
	}
}

func TestToolEmptyResults(t *testing.T) {
	t.Parallel()

	search := searchTool(t, Config{Backend: &fakeBackend{}})
	result, err := search.Exec(context.Background(), struct{}{}, json.RawMessage(`{"query":"nothing"}`))
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if result.Text != "No results." {
		t.Fatalf("result = %q", result.Text)
	}
}

func TestToolBadModelInputIsRecoverable(t *testing.T) {
	t.Parallel()

	search := searchTool(t, Config{Backend: &fakeBackend{}})
	cases := []string{`{"query":"  "}`, `{"query":"x","limit":-1}`, `{`}
	for _, args := range cases {
		_, err := search.Exec(context.Background(), struct{}{}, json.RawMessage(args))
		var failed *tool.Failed
		if !errors.As(err, &failed) {
			t.Fatalf("args %s: error = %v, want *tool.Failed", args, err)
		}
	}
}

func TestToolBackendFailureIsWrapped(t *testing.T) {
	t.Parallel()

	backend := &fakeBackend{err: errors.New("backend offline")}
	search := searchTool(t, Config{Backend: backend})
	_, err := search.Exec(context.Background(), struct{}{}, json.RawMessage(`{"query":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "backend offline") {
		t.Fatalf("error = %v, want the wrapped backend failure", err)
	}
	var failed *tool.Failed
	if errors.As(err, &failed) {
		t.Fatal("infrastructure failure was reported as recoverable model input")
	}
}

func TestNewRejectsDegenerateConfig(t *testing.T) {
	t.Parallel()

	if _, err := New[struct{}](Config{Timeout: -1}); err == nil {
		t.Fatal("negative timeout accepted")
	}
	if _, err := New[struct{}](Config{MaxResults: -1}); err == nil {
		t.Fatal("negative max results accepted")
	}
	if _, err := New[struct{}](Config{MaxResults: MaxResultsLimit + 1}); err == nil {
		t.Fatal("max results above the limit accepted")
	}
}

const duckHTML = `<html><body>
<div class="result results_links">
  <a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fone&amp;rut=abc">One &amp; Only</a>
  <a class="result__snippet" href="#">First <b>snippet</b>.</a>
</div>
<div class="result result--ad">
  <a class="result__a" href="https://ads.example/buy">Buy</a>
  <a class="result__snippet">Sponsored</a>
</div>
<div class="result">
  <a class='result-link' href='https://example.com/two'>Two</a>
  <td class='result-snippet'>Second snippet</td>
</div>
<div class="result">
  <a class="result__a" href="https://example.com/one">Duplicate</a>
  <a class="result__snippet">Duplicate snippet</a>
</div>
</body></html>`

func TestDuckDuckGoSearchParsesResults(t *testing.T) {
	t.Parallel()

	var gotQuery, gotAgent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		gotAgent = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(duckHTML))
	}))
	defer server.Close()

	results, err := (&DuckDuckGo{Endpoint: server.URL}).Search(context.Background(), "golem search", 5)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if gotQuery != "golem search" {
		t.Fatalf("query = %q", gotQuery)
	}
	if gotAgent != DefaultUserAgent {
		t.Fatalf("user agent = %q", gotAgent)
	}
	if len(results) != 2 {
		t.Fatalf("results = %#v, want two non-ad, non-duplicate results", results)
	}
	if results[0].Title != "One & Only" || results[0].URL != "https://example.com/one" || results[0].Snippet != "First snippet." {
		t.Fatalf("first result = %#v", results[0])
	}
	if results[1].Title != "Two" || results[1].URL != "https://example.com/two" || results[1].Snippet != "Second snippet" {
		t.Fatalf("second result = %#v", results[1])
	}
}

func TestDuckDuckGoSearchRespectsLimit(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(duckHTML))
	}))
	defer server.Close()

	results, err := (&DuckDuckGo{Endpoint: server.URL}).Search(context.Background(), "q", 1)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) != 1 || results[0].URL != "https://example.com/one" {
		t.Fatalf("results = %#v", results)
	}
}

func TestDuckDuckGoSearchStatusError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer server.Close()

	_, err := (&DuckDuckGo{Endpoint: server.URL}).Search(context.Background(), "q", 1)
	var status *StatusError
	if !errors.As(err, &status) || status.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("error = %v, want StatusError 429", err)
	}
}

func TestDuckDuckGoSearchHonorsCancellation(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(duckHTML))
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (&DuckDuckGo{Endpoint: server.URL}).Search(ctx, "q", 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestDuckDuckGoRejectsBadEndpoint(t *testing.T) {
	t.Parallel()

	if _, err := (&DuckDuckGo{Endpoint: "://bad"}).Search(context.Background(), "q", 1); err == nil {
		t.Fatal("invalid endpoint accepted")
	}
}
