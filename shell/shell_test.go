package shell

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
)

// commandArgs builds the model-produced arguments for a run.
func commandArgs(command string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"command": %q}`, command))
}

// skipOnWindows skips tests written in POSIX shell syntax.
func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell syntax")
	}
}

func TestNewValidatesConfig(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{"negative timeout", Config{Timeout: -time.Second}, "timeout must not be negative"},
		{"negative max bytes", Config{MaxBytes: -1}, "max bytes must not be negative"},
		{"env entry without equals", Config{Env: []string{"GREETING"}}, "KEY=VALUE"},
		{"missing dir", Config{Dir: filepath.Join(dir, "nope")}, "dir"},
		{"dir is a file", Config{Dir: file}, "is not a directory"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := New[struct{}](testCase.cfg); err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("New(%+v) error = %v, want containing %q", testCase.cfg, err, testCase.want)
			}
		})
	}

	running, err := New[struct{}](Config{})
	if err != nil {
		t.Fatalf("New(Config{}) error = %v", err)
	}
	if running.Name != ToolName {
		t.Errorf("Name = %q, want %q", running.Name, ToolName)
	}
	if running.Description != ToolDescription {
		t.Errorf("Description = %q, want the exported ToolDescription", running.Description)
	}
	if !json.Valid(running.Schema) {
		t.Errorf("Schema is not valid JSON: %s", running.Schema)
	}
	if running.MaxRetries != nil || running.Timeout != 0 || running.Sequential {
		t.Errorf("per-tool policy fields should stay zero to inherit agent settings, got %+v", running)
	}
}

func TestMustNew(t *testing.T) {
	if running := MustNew[struct{}](Config{}); running.Name != ToolName {
		t.Errorf("MustNew Name = %q, want %q", running.Name, ToolName)
	}
	defer func() {
		if recover() == nil {
			t.Error("MustNew with a negative timeout should panic")
		}
	}()
	MustNew[struct{}](Config{Timeout: -1})
}

func TestRunCapturesCombinedOutput(t *testing.T) {
	skipOnWindows(t)
	running := MustNew[struct{}](Config{})
	result, err := running.Exec(context.Background(), struct{}{}, commandArgs("echo out; echo err >&2"))
	if err != nil {
		t.Fatalf("Exec error = %v", err)
	}
	// Stdout and stderr share one pipe, so arrival order is the
	// command's write order.
	if result != "out\nerr\n" {
		t.Errorf("Exec result = %q, want combined %q", result, "out\nerr\n")
	}
}

func TestRunNonZeroExitIsSuccessfulResult(t *testing.T) {
	skipOnWindows(t)
	cases := []struct {
		name    string
		command string
		want    string
	}{
		{
			name:    "output then failure",
			command: "printf hi; exit 3",
			want:    "hi\n\n[shell: exited with status 3]",
		},
		{
			name:    "failure without output",
			command: "exit 3",
			want:    "[shell: exited with status 3]",
		},
	}
	running := MustNew[struct{}](Config{})
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			result, err := running.Exec(context.Background(), struct{}{}, commandArgs(testCase.command))
			if err != nil {
				t.Fatalf("Exec error = %v; a non-zero exit is evidence for the model, not a failure", err)
			}
			if result != testCase.want {
				t.Errorf("Exec result = %q, want %q", result, testCase.want)
			}
		})
	}
}

func TestRunOutputTruncated(t *testing.T) {
	skipOnWindows(t)
	running := MustNew[struct{}](Config{MaxBytes: 8})
	result, err := running.Exec(context.Background(), struct{}{}, commandArgs("printf "+strings.Repeat("a", 40)))
	if err != nil {
		t.Fatalf("Exec error = %v", err)
	}
	want := strings.Repeat("a", 8) + "\n\n[shell: output truncated at 8 bytes]"
	if result != want {
		t.Errorf("Exec result = %q, want %q", result, want)
	}
}

func TestRunTimeoutFails(t *testing.T) {
	skipOnWindows(t)
	running := MustNew[struct{}](Config{Timeout: 20 * time.Millisecond})
	_, err := running.Exec(context.Background(), struct{}{}, commandArgs("sleep 2"))
	if err == nil {
		t.Fatal("Exec should fail when the command exceeds its timeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Exec error = %v, want context.DeadlineExceeded in the chain", err)
	}
	var retry *model.ModelRetry
	if errors.As(err, &retry) {
		t.Error("a timeout must not be a correctable rejection")
	}
}

func TestRunBadArgumentsAreCorrectable(t *testing.T) {
	cases := []struct {
		name string
		args json.RawMessage
	}{
		{"not json", json.RawMessage(`{`)},
		{"not an object", json.RawMessage(`"echo hi"`)},
		{"missing command", json.RawMessage(`{}`)},
		{"empty command", json.RawMessage(`{"command":""}`)},
		{"non-string command", json.RawMessage(`{"command":42}`)},
	}
	running := MustNew[struct{}](Config{})
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := running.Exec(context.Background(), struct{}{}, testCase.args)
			var retry *model.ModelRetry
			if !errors.As(err, &retry) {
				t.Fatalf("Exec(%s) error = %v, want *model.ModelRetry in the chain", testCase.args, err)
			}
		})
	}
}

func TestRunEnvAndDir(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	running := MustNew[struct{}](Config{Dir: dir, Env: []string{"GREETING=hello"}})
	result, err := running.Exec(context.Background(), struct{}{}, commandArgs("echo $GREETING; touch marker.txt"))
	if err != nil {
		t.Fatalf("Exec error = %v", err)
	}
	if result != "hello\n" {
		t.Errorf("Exec result = %q, want %q", result, "hello\n")
	}
	if _, err := os.Stat(filepath.Join(dir, "marker.txt")); err != nil {
		t.Errorf("command should run in Config.Dir: %v", err)
	}
}

func TestRunHonorsCanceledContext(t *testing.T) {
	skipOnWindows(t)
	running := MustNew[struct{}](Config{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := running.Exec(ctx, struct{}{}, commandArgs("echo hi"))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Exec error = %v, want context.Canceled", err)
	}
}

func TestComposesWithAgent(t *testing.T) {
	skipOnWindows(t)
	running := MustNew[struct{}](Config{})
	client := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: ToolName, Args: commandArgs("echo hello from the tool")},
		}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "the tool said hello"}},
	)
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithTools[struct{}, string](running),
	)
	if err != nil {
		t.Fatalf("golem.New error = %v", err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "run it")
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if result.Output != "the tool said hello" {
		t.Errorf("Output = %q, want %q", result.Output, "the tool said hello")
	}
	var toolEvidence string
	for _, message := range result.Messages {
		if message.Role == model.RoleTool {
			toolEvidence = message.Content
		}
	}
	if toolEvidence != "hello from the tool\n" {
		t.Errorf("tool result message = %q, want the command output", toolEvidence)
	}
}
