// Package tokens defines the provider-neutral contract for input-token
// counting: asking a provider how many tokens a request would consume
// before sending it. Provider adapters live under providers; tokenizers
// and tokenizer libraries stay application concerns.
package tokens

import (
	"context"

	"github.com/abubakarsiddik31/golem/model"
)

// Counter reports a provider's input-token count for a would-be
// request. Implementations exist only where the provider offers a
// counting endpoint; see the providers guide for the support matrix.
// Implementations must honor ctx cancellation and return errors rather
// than logging them as a substitute for propagation.
type Counter interface {
	// CountTokens prices one request: the token count the provider
	// reports for the messages, plus the tools where the provider counts
	// tool definitions. The result never includes a would-be response.
	CountTokens(ctx context.Context, input CountInput) (int, error)
}

// CountInput is the request shape a counter prices: the conversation a
// request would carry, plus the tools it would advertise.
type CountInput struct {
	// Messages is the full conversation the request would carry, system
	// messages included, exactly as it would be sent.
	Messages []model.Message
	// Tools is the tool set the request would advertise; empty means no
	// tools. Providers that do not count tool definitions ignore the
	// field — the count is then a lower bound on the real request.
	Tools []model.ToolSpec
}
