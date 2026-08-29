package tool

import (
	"context"
	"fmt"
)

// DeferKind distinguishes why a tool deferred its execution.
type DeferKind string

const (
	// DeferApproval pauses the run until the application relays a human
	// decision on the call. The tool gates its side effects behind
	// CallApproved so the action happens only after approval.
	DeferApproval DeferKind = "approval"
	// DeferExternal pauses the run until the application supplies the
	// call's result, typically because it is produced outside the agent
	// process.
	DeferExternal DeferKind = "external"
)

// Deferred is returned by a tool's Exec to pause the run instead of
// producing a result. The run ends with the call left pending — grouped
// under Result.Pending by kind — and the application resumes the
// conversation by resolving it: approvals re-execute Exec with the
// approved marker set (see CallApproved), external results are handed
// to the model verbatim. Reason is the tool's explanation to whoever
// resolves the request, such as the approval prompt text or a
// correlation key for the external job.
type Deferred struct {
	Kind   DeferKind
	Reason string
}

// Error implements error so the sentinel rides the Exec error channel,
// wrapping like any other classified tool error.
func (d *Deferred) Error() string {
	return fmt.Sprintf("tool deferred (%s): %s", d.Kind, d.Reason)
}

// approvedCallKey marks a context as the approved re-execution of a
// call that deferred for approval.
type approvedCallKey struct{}

// CallApproved reports whether the current execution re-runs a call
// that previously deferred for approval and has been approved. A tool
// that defers with DeferApproval must check this before its side
// effects: the deferred pass and the approved re-run share one Exec.
func CallApproved(ctx context.Context) bool {
	approved, _ := ctx.Value(approvedCallKey{}).(bool)
	return approved
}

// WithApprovedCall returns a context that marks the execution as the
// approved re-run of a deferred call. It is runtime plumbing for the
// execution loop; applications and tools read the marker with
// CallApproved.
func WithApprovedCall(ctx context.Context) context.Context {
	return context.WithValue(ctx, approvedCallKey{}, true)
}
