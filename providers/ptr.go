// Package providers holds Golem's provider adapters and small shared
// helpers for configuring them. Each subpackage adapts one provider API
// to the provider-neutral model contract; this parent package exists
// only for helpers every adapter config shares.
package providers

// Ptr returns a pointer to v. It builds the optional Config fields whose
// zero value is meaningful, such as a Temperature of 0: a nil field
// leaves the provider default, so the value must be set by address.
func Ptr[T any](v T) *T {
	return &v
}
