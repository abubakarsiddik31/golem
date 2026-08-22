package webfetch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
)

// urlArgs builds the model-produced arguments for a fetch.
func urlArgs(url string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"url": %q}`, url))
}

func TestNewValidatesConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{"negative timeout", Config{Timeout: -time.Second}, "timeout must not be negative"},
		{"negative max bytes", Config{MaxBytes: -1}, "max bytes must not be negative"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := New[struct{}](testCase.cfg); err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("New(%+v) error = %v, want containing %q", testCase.cfg, err, testCase.want)
			}
		})
	}

	fetched, err := New[struct{}](Config{})
	if err != nil {
		t.Fatalf("New(Config{}) error = %v", err)
	}
	if fetched.Name != ToolName {
		t.Errorf("Name = %q, want %q", fetched.Name, ToolName)
	}
	if fetched.Description != ToolDescription {
		t.Errorf("Description = %q, want the exported ToolDescription", fetched.Description)
	}
	if !json.Valid(fetched.Schema) {
		t.Errorf("Schema is not valid JSON: %s", fetched.Schema)
	}
	if fetched.Exec == nil {
		t.Error("Exec is nil")
	}
	if fetched.MaxRetries != nil || fetched.Timeout != 0 || fetched.Sequential {
		t.Errorf("per-tool policy fields should stay zero to inherit agent settings, got %+v", fetched)
	}
}

func TestMustNew(t *testing.T) {
	if fetched := MustNew[struct{}](Config{}); fetched.Name != ToolName {
		t.Errorf("MustNew Name = %q, want %q", fetched.Name, ToolName)
	}
	defer func() {
		if recover() == nil {
			t.Error("MustNew with a negative timeout should panic")
		}
	}()
	MustNew[struct{}](Config{Timeout: -1})
}

func TestFetchExtractsHTML(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<html><head><title>Docs</title><script>var x = 1;</script></head>"+
			"<body><h1>Golem</h1><p>Tools &amp; agents</p></body></html>")
	}))
	defer server.Close()

	fetched := MustNew[struct{}](Config{})
	result, err := fetched.Exec(context.Background(), struct{}{}, urlArgs(server.URL))
	if err != nil {
		t.Fatalf("Exec error = %v", err)
	}
	want := "Docs\nGolem\nTools & agents"
	if result != want {
		t.Errorf("Exec result = %q, want %q", result, want)
	}
}

func TestFetchPassesThroughTextTypes(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
	}{
		{"plain text", "text/plain; charset=utf-8"},
		{"json", "application/json"},
		{"xml", "application/xml"},
		{"csv", "text/csv"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", testCase.contentType)
				fmt.Fprint(w, "body { passthrough: true }\n")
			}))
			defer server.Close()

			fetched := MustNew[struct{}](Config{})
			result, err := fetched.Exec(context.Background(), struct{}{}, urlArgs(server.URL))
			if err != nil {
				t.Fatalf("Exec error = %v", err)
			}
			if want := "body { passthrough: true }\n"; result != want {
				t.Errorf("Exec result = %q, want the body unchanged %q", result, want)
			}
		})
	}
}

func TestFetchNon2xxFailsWithStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "missing", http.StatusNotFound)
	}))
	defer server.Close()

	fetched := MustNew[struct{}](Config{})
	_, err := fetched.Exec(context.Background(), struct{}{}, urlArgs(server.URL))
	if err == nil {
		t.Fatal("Exec should fail on a non-2xx response")
	}
	var statusErr *StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Exec error = %v, want *StatusError in the chain", err)
	}
	if statusErr.StatusCode != http.StatusNotFound || statusErr.URL != server.URL {
		t.Errorf("StatusError = %+v, want status 404 for %q", statusErr, server.URL)
	}
	var retry *model.ModelRetry
	if errors.As(err, &retry) {
		t.Error("a transport-level status failure must not be correctable")
	}
}

func TestFetchUnsupportedContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		fmt.Fprint(w, "binary-ish payload")
	}))
	defer server.Close()

	fetched := MustNew[struct{}](Config{})
	_, err := fetched.Exec(context.Background(), struct{}{}, urlArgs(server.URL))
	var typeErr *UnsupportedContentTypeError
	if !errors.As(err, &typeErr) {
		t.Fatalf("Exec error = %v, want *UnsupportedContentTypeError in the chain", err)
	}
	if typeErr.ContentType != "image/png" {
		t.Errorf("ContentType = %q, want image/png", typeErr.ContentType)
	}
}

// roundTripFunc adapts a function to http.RoundTripper so tests can
// hand the tool exact responses an httptest server cannot produce —
// Go's server sniffs a Content-Type on every unlabeled write.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestFetchMissingContentTypeRejected(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("unlabeled bytes")),
		}, nil
	})}
	fetched := MustNew[struct{}](Config{HTTPClient: client})
	_, err := fetched.Exec(context.Background(), struct{}{}, urlArgs("https://example.com/unlabeled"))
	var typeErr *UnsupportedContentTypeError
	if !errors.As(err, &typeErr) {
		t.Fatalf("Exec error = %v, want *UnsupportedContentTypeError for an unlabeled body", err)
	}
	if typeErr.ContentType != "" {
		t.Errorf("ContentType = %q, want empty for a missing header", typeErr.ContentType)
	}
}

func TestFetchTruncatesLargeBodies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, strings.Repeat("a", 100))
	}))
	defer server.Close()

	fetched := MustNew[struct{}](Config{MaxBytes: 16})
	result, err := fetched.Exec(context.Background(), struct{}{}, urlArgs(server.URL))
	if err != nil {
		t.Fatalf("Exec error = %v", err)
	}
	want := strings.Repeat("a", 16) + "\n\n[webfetch: response truncated at 16 bytes]"
	if result != want {
		t.Errorf("Exec result = %q, want %q", result, want)
	}
}

func TestFetchTimeoutIsNotCorrectable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		fmt.Fprint(w, "late")
	}))
	defer server.Close()

	fetched := MustNew[struct{}](Config{Timeout: time.Millisecond})
	_, err := fetched.Exec(context.Background(), struct{}{}, urlArgs(server.URL))
	if err == nil {
		t.Fatal("Exec should fail when the fetch exceeds its timeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Exec error = %v, want context.DeadlineExceeded in the chain", err)
	}
	var retry *model.ModelRetry
	if errors.As(err, &retry) {
		t.Error("cancellation must not be a correctable rejection")
	}
}

func TestBadArgumentsAreCorrectable(t *testing.T) {
	cases := []struct {
		name string
		args json.RawMessage
	}{
		{"not json", json.RawMessage(`{`)},
		{"not an object", json.RawMessage(`"https://example.com"`)},
		{"missing url", json.RawMessage(`{}`)},
		{"empty url", json.RawMessage(`{"url":""}`)},
		{"non-string url", json.RawMessage(`{"url":42}`)},
		{"non-http scheme", json.RawMessage(`{"url":"ftp://example.com/doc"}`)},
		{"relative path", json.RawMessage(`{"url":"/docs"}`)},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fetched := MustNew[struct{}](Config{})
			_, err := fetched.Exec(context.Background(), struct{}{}, testCase.args)
			var retry *model.ModelRetry
			if !errors.As(err, &retry) {
				t.Fatalf("Exec(%s) error = %v, want *model.ModelRetry in the chain", testCase.args, err)
			}
		})
	}
}

func TestUserAgent(t *testing.T) {
	var got []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("User-Agent"))
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "ok")
	}))
	defer server.Close()

	byDefault := MustNew[struct{}](Config{})
	if _, err := byDefault.Exec(context.Background(), struct{}{}, urlArgs(server.URL)); err != nil {
		t.Fatalf("default client Exec error = %v", err)
	}
	custom := MustNew[struct{}](Config{UserAgent: "research-bot/1"})
	if _, err := custom.Exec(context.Background(), struct{}{}, urlArgs(server.URL)); err != nil {
		t.Fatalf("custom client Exec error = %v", err)
	}
	want := []string{DefaultUserAgent, "research-bot/1"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("User-Agent headers = %v, want %v", got, want)
	}
}

func TestComposesWithAgent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<h1>Release notes</h1><ul><li>web fetch</li><li>run events</li></ul>")
	}))
	defer server.Close()

	fetched := MustNew[struct{}](Config{})
	client := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: ToolName, Args: urlArgs(server.URL)},
		}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "two features shipped"}},
	)
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithTools[struct{}, string](fetched),
	)
	if err != nil {
		t.Fatalf("golem.New error = %v", err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "what shipped?")
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if result.Output != "two features shipped" {
		t.Errorf("Output = %q, want %q", result.Output, "two features shipped")
	}
	var toolEvidence string
	for _, message := range result.Messages {
		if message.Role == model.RoleTool {
			toolEvidence = message.Content
		}
	}
	want := "Release notes\nweb fetch\nrun events"
	if toolEvidence != want {
		t.Errorf("tool result message = %q, want %q", toolEvidence, want)
	}
}
