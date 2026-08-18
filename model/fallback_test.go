package model_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
)

// retryableErr reports a caller-attemptable failure, like an adapter's
// classification of HTTP 429 and 5xx responses.
type retryableErr struct{ retryable bool }

func (e retryableErr) Error() string   { return "retryable failure" }
func (e retryableErr) Retryable() bool { return e.retryable }

// fallbackFake is a counting model that answers with scripted output or
// error. fragments, when set, makes it a StreamingModel that emits those
// fragments through onDelta before returning.
type fallbackFake struct {
	name      string
	response  string
	err       error
	fragments []model.Delta

	calls int
}

func (f *fallbackFake) Generate(ctx context.Context, request model.Request) (model.Response, error) {
	f.calls++
	if f.err != nil {
		return model.Response{}, f.err
	}
	return model.Response{Message: model.Message{Role: model.RoleAssistant, Content: f.response}}, nil
}

func (f *fallbackFake) GenerateStream(ctx context.Context, request model.Request, onDelta func(model.Delta) error) (model.Response, error) {
	for _, fragment := range f.fragments {
		if onDelta != nil {
			if err := onDelta(fragment); err != nil {
				return model.Response{}, err
			}
		}
	}
	f.fragments = nil // replaying on a later call would falsify the test
	return f.Generate(ctx, request)
}

func TestFallbackTriesModelsInOrder(t *testing.T) {
	t.Parallel()

	primary := &fallbackFake{name: "primary", err: retryableErr{retryable: true}}
	backup := &fallbackFake{name: "backup", response: "from backup"}
	fallback, err := model.NewFallback(primary, backup)
	if err != nil {
		t.Fatalf("NewFallback() error = %v", err)
	}

	response, err := fallback.Generate(context.Background(), model.Request{})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if response.Message.Content != "from backup" {
		t.Fatalf("response = %q, want the backup's answer", response.Message.Content)
	}
	if primary.calls != 1 || backup.calls != 1 {
		t.Fatalf("calls = primary %d, backup %d; want 1 and 1", primary.calls, backup.calls)
	}
}

func TestFallbackReturnsFirstSuccessWithoutTouchingAlternates(t *testing.T) {
	t.Parallel()

	primary := &fallbackFake{name: "primary", response: "fast"}
	backup := &fallbackFake{name: "backup", response: "unused"}
	fallback, _ := model.NewFallback(primary, backup)

	if _, err := fallback.Generate(context.Background(), model.Request{}); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if backup.calls != 0 {
		t.Fatalf("backup called %d times, want 0", backup.calls)
	}
}

func TestFallbackStopsOnNonRetryableFailure(t *testing.T) {
	t.Parallel()

	cause := errors.New("bad request")
	primary := &fallbackFake{name: "primary", err: fmt.Errorf("wrapped: %w", cause)}
	backup := &fallbackFake{name: "backup"}
	fallback, _ := model.NewFallback(primary, backup)

	_, err := fallback.Generate(context.Background(), model.Request{})
	if !errors.Is(err, cause) {
		t.Fatalf("Generate() error = %v, want the primary's cause preserved", err)
	}
	if backup.calls != 0 {
		t.Fatalf("backup called %d times, want 0 after a non-retryable failure", backup.calls)
	}
}

func TestFallbackReturnsLastErrorWhenAllModelsFail(t *testing.T) {
	t.Parallel()

	primary := &fallbackFake{name: "primary", err: retryableErr{retryable: true}}
	backup := &fallbackFake{name: "backup", err: errors.New("backup down")}
	fallback, _ := model.NewFallback(primary, backup)

	_, err := fallback.Generate(context.Background(), model.Request{})
	if got := err.Error(); got != "backup down" {
		t.Fatalf("final error = %q, want the backup's error unchanged", got)
	}
	if model.IsRetryable(err) {
		t.Fatalf("final error = %v; the backup's non-retryable classification must survive", err)
	}
}

func TestFallbackPrefersCancellationOverContinuing(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	primary := &fallbackFake{name: "primary", err: retryableErr{retryable: true}}
	backup := &fallbackFake{name: "backup"}
	fallback, _ := model.NewFallback(primary, backup)
	cancel()

	_, err := fallback.Generate(ctx, model.Request{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Generate() error = %v, want context.Canceled", err)
	}
	if backup.calls != 0 {
		t.Fatalf("backup called %d times, want 0 after cancellation", backup.calls)
	}
}

func TestNewFallbackValidatesItsModels(t *testing.T) {
	t.Parallel()

	working := &fallbackFake{name: "working"}
	if _, err := model.NewFallback(nil, working); err == nil {
		t.Fatal("nil primary accepted")
	}
	if _, err := model.NewFallback(working, nil); err == nil {
		t.Fatal("nil alternate accepted")
	}
	if _, err := model.NewFallback(working); err == nil {
		t.Fatal("zero alternates accepted")
	}
	if _, err := model.NewFallback(working, &fallbackFake{name: "backup"}); err != nil {
		t.Fatalf("valid construction rejected: %v", err)
	}
}

func TestFallbackStreamFallsBackOnlyBeforeFirstFragment(t *testing.T) {
	t.Parallel()

	t.Run("failure before any fragment moves to the backup", func(t *testing.T) {
		t.Parallel()

		primary := &fallbackFake{name: "primary", err: retryableErr{retryable: true}}
		backup := &fallbackFake{name: "backup", response: "ok",
			fragments: []model.Delta{{Content: "ok"}}}
		fallback, _ := model.NewFallback(primary, backup)

		var forwarded []string
		response, err := fallback.GenerateStream(context.Background(), model.Request{},
			func(delta model.Delta) error {
				forwarded = append(forwarded, delta.Content)
				return nil
			})
		if err != nil {
			t.Fatalf("GenerateStream() error = %v", err)
		}
		if response.Message.Content != "ok" || len(forwarded) != 1 || forwarded[0] != "ok" {
			t.Fatalf("response = %q, forwarded = %v; want the backup's stream exactly once", response.Message.Content, forwarded)
		}
	})

	t.Run("failure after a fragment returns the error", func(t *testing.T) {
		t.Parallel()

		primary := &fallbackFake{name: "primary", err: retryableErr{retryable: true},
			fragments: []model.Delta{{Content: "partial"}}}
		backup := &fallbackFake{name: "backup", response: "unused"}
		fallback, _ := model.NewFallback(primary, backup)

		var forwarded int
		_, err := fallback.GenerateStream(context.Background(), model.Request{},
			func(delta model.Delta) error {
				forwarded++
				return nil
			})
		if !errors.Is(err, retryableErr{retryable: true}) {
			t.Fatalf("GenerateStream() error = %v, want the primary's failure", err)
		}
		if backup.calls != 0 {
			t.Fatal("backup used after fragments were already forwarded; a fallback would replay them")
		}
		if forwarded != 1 {
			t.Fatalf("forwarded %d fragments, want the 1 already-seen fragment", forwarded)
		}
	})

	t.Run("a non-streaming member is a configuration error", func(t *testing.T) {
		t.Parallel()

		streamer := &fallbackFake{name: "streamer", response: "ok",
			fragments: []model.Delta{{Content: "ok"}}}
		plain := plainModel{}
		fallback, _ := model.NewFallback(streamer, plain)

		if _, err := fallback.GenerateStream(context.Background(), model.Request{}, nil); err == nil {
			t.Fatal("non-streaming member accepted in a streaming run")
		}
	})
}

// plainModel implements only model.Model.
type plainModel struct{}

func (plainModel) Generate(ctx context.Context, request model.Request) (model.Response, error) {
	return model.Response{}, nil
}
