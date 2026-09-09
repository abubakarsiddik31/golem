package testmodel

import (
	"context"

	"github.com/abubakarsiddik31/golem/tokens"
)

// CountFunc adapts a function to the tokens.Counter port: the function
// receives the exact input the caller passed and decides the count or
// the error.
type CountFunc func(ctx context.Context, input tokens.CountInput) (int, error)

var _ tokens.Counter = CountFunc(nil)

// CountTokens delegates to f.
func (f CountFunc) CountTokens(ctx context.Context, input tokens.CountInput) (int, error) {
	return f(ctx, input)
}
