package fileread

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
)

// pathArgs builds the model-produced arguments for a read.
func pathArgs(path string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"path": %q}`, path))
}

// writeRoot creates a temp root with one nested text file and returns
// the root directory.
func writeRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes", "release.md"), []byte("web fetch shipped\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestNewValidatesConfig(t *testing.T) {
	root := writeRoot(t)
	file := filepath.Join(root, "notes", "release.md")

	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{"negative max bytes", Config{Root: root, MaxBytes: -1}, "max bytes must not be negative"},
		{"missing root", Config{}, "root is required"},
		{"root is a file", Config{Root: file}, "is not a directory"},
		{"root does not exist", Config{Root: filepath.Join(root, "nope")}, "root"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := New[struct{}](testCase.cfg); err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("New(%+v) error = %v, want containing %q", testCase.cfg, err, testCase.want)
			}
		})
	}

	reading, err := New[struct{}](Config{Root: root})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	if reading.Name != ToolName {
		t.Errorf("Name = %q, want %q", reading.Name, ToolName)
	}
	if reading.Description != ToolDescription {
		t.Errorf("Description = %q, want the exported ToolDescription", reading.Description)
	}
	if !json.Valid(reading.Schema) {
		t.Errorf("Schema is not valid JSON: %s", reading.Schema)
	}
	if reading.MaxRetries != nil || reading.Timeout != 0 || reading.Sequential {
		t.Errorf("per-tool policy fields should stay zero to inherit agent settings, got %+v", reading)
	}
}

func TestMustNew(t *testing.T) {
	root := writeRoot(t)
	if reading := MustNew[struct{}](Config{Root: root}); reading.Name != ToolName {
		t.Errorf("MustNew Name = %q, want %q", reading.Name, ToolName)
	}
	defer func() {
		if recover() == nil {
			t.Error("MustNew without a root should panic")
		}
	}()
	MustNew[struct{}](Config{})
}

func TestReadNestedTextFile(t *testing.T) {
	root := writeRoot(t)
	reading := MustNew[struct{}](Config{Root: root})
	result, err := reading.Exec(context.Background(), struct{}{}, pathArgs("notes/release.md"))
	if err != nil {
		t.Fatalf("Exec error = %v", err)
	}
	if result != "web fetch shipped\n" {
		t.Errorf("Exec result = %q, want the file unchanged", result)
	}
}

func TestReadConfinedToRoot(t *testing.T) {
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Symlink(outsideFile, filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}

	reading := MustNew[struct{}](Config{Root: root})
	cases := []struct {
		name string
		path string
	}{
		{"absolute path", "/etc/passwd"},
		{"parent traversal", "../secret.txt"},
		{"nested traversal", "a/../../secret.txt"},
		{"symlink escaping the root", "link.txt"},
		{"missing file", "notes/release.md"},
		{"directory", "."},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := reading.Exec(context.Background(), struct{}{}, pathArgs(testCase.path))
			var retry *model.ModelRetry
			if !errors.As(err, &retry) {
				t.Fatalf("Exec(%q) error = %v, want *model.ModelRetry in the chain", testCase.path, err)
			}
		})
	}
}

func TestReadBadArgumentsAreCorrectable(t *testing.T) {
	root := writeRoot(t)
	reading := MustNew[struct{}](Config{Root: root})
	cases := []struct {
		name string
		args json.RawMessage
	}{
		{"not json", json.RawMessage(`{`)},
		{"not an object", json.RawMessage(`"notes/release.md"`)},
		{"missing path", json.RawMessage(`{}`)},
		{"empty path", json.RawMessage(`{"path":""}`)},
		{"non-string path", json.RawMessage(`{"path":42}`)},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := reading.Exec(context.Background(), struct{}{}, testCase.args)
			var retry *model.ModelRetry
			if !errors.As(err, &retry) {
				t.Fatalf("Exec(%s) error = %v, want *model.ModelRetry in the chain", testCase.args, err)
			}
		})
	}
}

func TestReadBinaryRejected(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "blob.bin"), []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, 0o644); err != nil {
		t.Fatal(err)
	}

	reading := MustNew[struct{}](Config{Root: root})
	_, err := reading.Exec(context.Background(), struct{}{}, pathArgs("blob.bin"))
	var typeErr *UnsupportedContentError
	if !errors.As(err, &typeErr) {
		t.Fatalf("Exec error = %v, want *UnsupportedContentError in the chain", err)
	}
	if typeErr.ContentType != "image/png" {
		t.Errorf("ContentType = %q, want image/png", typeErr.ContentType)
	}
	var retry *model.ModelRetry
	if errors.As(err, &retry) {
		t.Error("binary content is an environment fact, not a correctable rejection")
	}
}

func TestReadTruncatesLargeFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "long.txt"), []byte(strings.Repeat("a", 100)), 0o644); err != nil {
		t.Fatal(err)
	}

	reading := MustNew[struct{}](Config{Root: root, MaxBytes: 16})
	result, err := reading.Exec(context.Background(), struct{}{}, pathArgs("long.txt"))
	if err != nil {
		t.Fatalf("Exec error = %v", err)
	}
	want := strings.Repeat("a", 16) + "\n\n[fileread: file truncated at 16 bytes]"
	if result != want {
		t.Errorf("Exec result = %q, want %q", result, want)
	}
}

func TestReadHonorsCanceledContext(t *testing.T) {
	root := writeRoot(t)
	reading := MustNew[struct{}](Config{Root: root})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := reading.Exec(ctx, struct{}{}, pathArgs("notes/release.md"))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Exec error = %v, want context.Canceled", err)
	}
}

func TestComposesWithAgent(t *testing.T) {
	root := writeRoot(t)
	reading := MustNew[struct{}](Config{Root: root})
	client := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: ToolName, Args: pathArgs("notes/release.md")},
		}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "web fetch shipped"}},
	)
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithTools[struct{}, string](reading),
	)
	if err != nil {
		t.Fatalf("golem.New error = %v", err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "what shipped?")
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if result.Output != "web fetch shipped" {
		t.Errorf("Output = %q, want %q", result.Output, "web fetch shipped")
	}
	var toolEvidence string
	for _, message := range result.Messages {
		if message.Role == model.RoleTool {
			toolEvidence = message.Content
		}
	}
	if toolEvidence != "web fetch shipped\n" {
		t.Errorf("tool result message = %q, want the file content", toolEvidence)
	}
}
