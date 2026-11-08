package guard_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem/guard"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

func TestApplyInputChain(t *testing.T) {
	ctx := context.Background()
	redact := guard.InputGuard(func(_ context.Context, prompt string) guard.Result {
		if strings.Contains(prompt, "hunter2") {
			return guard.Replace(strings.ReplaceAll(prompt, "hunter2", "[redacted]"))
		}
		return guard.Allow()
	})
	offTopic := guard.InputGuard(func(_ context.Context, prompt string) guard.Result {
		if strings.Contains(prompt, "weather") {
			return guard.Block("off-topic")
		}
		return guard.Allow()
	})

	got, blocked := guard.ApplyInput(ctx, []guard.InputGuard{redact, offTopic}, "my password is hunter2")
	if blocked != nil || got != "my password is [redacted]" {
		t.Fatalf("ApplyInput = %q, %v; want replaced prompt, nil", got, blocked)
	}
	// A block ends the chain: the redaction still happened on the way
	// in, and the reason is what the refusal carries.
	_, blocked = guard.ApplyInput(ctx, []guard.InputGuard{redact, offTopic}, "weather + hunter2")
	if blocked == nil || blocked.Reason != "off-topic" {
		t.Fatalf("ApplyInput blocked = %v; want off-topic", blocked)
	}
	// A Retry verdict is invalid for input: fail-closed block naming
	// the misuse.
	invalid := guard.InputGuard(func(_ context.Context, prompt string) guard.Result {
		return guard.Retry("nope")
	})
	_, blocked = guard.ApplyInput(ctx, []guard.InputGuard{invalid}, "hello")
	if blocked == nil || !strings.Contains(blocked.Reason, "fail-closed") {
		t.Fatalf("ApplyInput invalid verdict = %v; want fail-closed block", blocked)
	}
}

func TestApplyToolCallChain(t *testing.T) {
	ctx := context.Background()
	narrow := guard.ToolGuard(func(_ context.Context, call model.ToolCall) guard.Result {
		if call.Name == "run" {
			return guard.Replace(`{"command":"echo allowed"}`)
		}
		return guard.Allow()
	})
	deny := guard.ToolGuard(func(_ context.Context, call model.ToolCall) guard.Result {
		return guard.Block("not allowed")
	})

	call := model.ToolCall{ID: "c1", Name: "run", Args: json.RawMessage(`{"command":"rm -rf /"}`)}
	got, err := guard.ApplyToolCall(ctx, []guard.ToolGuard{narrow}, call)
	if err != nil || string(got.Args) != `{"command":"echo allowed"}` {
		t.Fatalf("ApplyToolCall = %s, %v; want replaced args, nil", got.Args, err)
	}
	// The replacement threads into the guards that follow.
	seen := ""
	recorder := guard.ToolGuard(func(_ context.Context, c model.ToolCall) guard.Result {
		seen = string(c.Args)
		return guard.Allow()
	})
	got, err = guard.ApplyToolCall(ctx, []guard.ToolGuard{narrow, recorder}, call)
	if err != nil || seen != `{"command":"echo allowed"}` {
		t.Fatalf("ApplyToolCall replacement did not thread: %q, %v", seen, err)
	}
	_, err = guard.ApplyToolCall(ctx, []guard.ToolGuard{deny}, call)
	var blocked *guard.Blocked
	if !errors.As(err, &blocked) || blocked.Reason != "not allowed" {
		t.Fatalf("ApplyToolCall block = %v; want *guard.Blocked", err)
	}
	retryGuard := guard.ToolGuard(func(_ context.Context, call model.ToolCall) guard.Result {
		return guard.Retry("bad command shape")
	})
	_, err = guard.ApplyToolCall(ctx, []guard.ToolGuard{retryGuard}, call)
	var retry *model.ModelRetry
	if !errors.As(err, &retry) {
		t.Fatalf("ApplyToolCall retry = %v; want *model.ModelRetry", err)
	}
}

func TestApplyToolResultChain(t *testing.T) {
	ctx := context.Background()
	call := model.ToolCall{ID: "c1", Name: "fetch"}
	scrub := guard.ResultGuard(func(_ context.Context, call model.ToolCall, result tool.Result) guard.Result {
		if strings.Contains(result.Text, "SECRET") {
			return guard.Replace(strings.ReplaceAll(result.Text, "SECRET", "[redacted]"))
		}
		return guard.Allow()
	})
	stop := guard.ResultGuard(func(_ context.Context, call model.ToolCall, result tool.Result) guard.Result {
		return guard.Block("external content")
	})

	got, blocked := guard.ApplyToolResult(ctx, []guard.ResultGuard{scrub}, call, tool.Text("key=SECRET"))
	if blocked != nil || got.Text != "key=[redacted]" {
		t.Fatalf("ApplyToolResult = %q, %v; want scrubbed, nil", got.Text, blocked)
	}
	got, blocked = guard.ApplyToolResult(ctx, []guard.ResultGuard{stop}, call, tool.Text("hello"))
	if blocked == nil || blocked.Reason != "external content" {
		t.Fatalf("ApplyToolResult blocked = %v; want external content", blocked)
	}
	if !strings.Contains(got.Text, "blocked by policy") || !strings.Contains(got.Text, "external content") {
		t.Fatalf("refusal text %q missing reason", got.Text)
	}
}

func TestApplyOutputChain(t *testing.T) {
	ctx := context.Background()
	pii := guard.OutputGuard(func(_ context.Context, content string) guard.Result {
		if strings.Contains(content, "SSN") {
			return guard.Block("personal data")
		}
		return guard.Allow()
	})
	got, blocked := guard.ApplyOutput(ctx, []guard.OutputGuard{pii}, "all clear")
	if blocked != nil || got != "all clear" {
		t.Fatalf("ApplyOutput = %q, %v; want passthrough", got, blocked)
	}
	_, blocked = guard.ApplyOutput(ctx, []guard.OutputGuard{pii}, "SSN inside")
	if blocked == nil || blocked.Reason != "personal data" {
		t.Fatalf("ApplyOutput blocked = %v; want personal data", blocked)
	}
}

func TestRefusalWording(t *testing.T) {
	if got := guard.Refusal("secret keys"); !strings.Contains(got, "blocked by policy") || !strings.Contains(got, "secret keys") {
		t.Fatalf("Refusal = %q", got)
	}
	if got := guard.Refusal(""); !strings.Contains(got, "no reason given") {
		t.Fatalf("Refusal empty = %q", got)
	}
}
