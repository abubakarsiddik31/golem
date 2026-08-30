package tool_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/abubakarsiddik31/golem/tool"
)

func TestDeferredCarriesKindAndReason(t *testing.T) {
	t.Parallel()

	sentinel := &tool.Deferred{Kind: tool.DeferApproval, Reason: "deletes need sign-off"}
	wrapped := fmt.Errorf("gated: %w", sentinel)

	var deferred *tool.Deferred
	if !errors.As(wrapped, &deferred) {
		t.Fatalf("errors.As(wrapped, &deferred) = false, want the wrapped sentinel")
	}
	if deferred.Kind != tool.DeferApproval || deferred.Reason != "deletes need sign-off" {
		t.Fatalf("deferred = %+v, want the wrapped kind and reason", deferred)
	}
	if sentinel.Error() == "" {
		t.Fatal("Error() is empty")
	}
}

func TestCallApprovedReadsTheRuntimeMarker(t *testing.T) {
	t.Parallel()

	if tool.CallApproved(context.Background()) {
		t.Fatal("CallApproved(background) = true, want false")
	}
	if !tool.CallApproved(tool.WithApprovedCall(context.Background())) {
		t.Fatal("CallApproved(WithApprovedCall(ctx)) = false, want true")
	}
}
