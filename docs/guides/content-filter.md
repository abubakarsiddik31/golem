# Content filter

## Purpose

Turn a provider's safety-filter stop into a typed run failure, so an
application can distinguish a refused response from an ordinary answer
without parsing text.

## When to use

Compliance, moderation, and audit paths that must not treat refusal or
partial text as a successful answer. For general chat, the default is
usually enough: the response is decoded like any other and its cause
stays visible on `Result.FinishReason`.

## How it works

Providers report a safety stop as `model.FinishContentFilter`. By
default Golem preserves v1 behavior: the response is ordinary output —
its text, possibly empty or partial, is decoded — and the cause is
reported on `Result.FinishReason`. Silently discarding provider text
would be a surprising behavior change.

`WithContentFilterError` opts into failing the run whenever the finish
reason is `FinishContentFilter`, even when refusal or partial text comes
back. The run ends before any tool calls in that response execute, with
`RunError.Stage == StageContentFilter` wrapping a
`*ContentFilterError`. `errors.As` reaches the typed error, whose
`Message` field holds the filtered assistant response exactly as the
provider returned it. `RunError.Partial` carries the same response in
the transcript, so a resume or an audit log keeps the evidence.

The check happens before output decoding and before tool dispatch. A
filtered response with a tool call therefore stops the run rather than
executing the call. Streaming and plain runs share the check.

## Example

[`examples/content-filter`](../../examples/content-filter/main.go) runs
offline against a scripted model and shows both policies:

```go
agent, err := golem.New[struct{}, string](client, decoder,
	golem.WithContentFilterError[struct{}, string](),
)
_, err = agent.Run(ctx, golem.RunContext[struct{}]{}, "prompt")

var runErr *golem.RunError
var filterErr *golem.ContentFilterError
errors.As(err, &runErr)     // runErr.Stage == golem.StageContentFilter
errors.As(err, &filterErr) // filterErr.Message.Content
```

## API surface

- `golem.WithContentFilterError[Deps, Output]()` — opt into the typed failure.
- `golem.StageContentFilter` — the run stage for the failure.
- `golem.ContentFilterError{Message model.Message}` — the typed error.
- `model.FinishContentFilter` and `Result.FinishReason` — the default, non-failing signal.

## Gotchas

- The option is off by default; enabling it changes results for prompts
  the provider filters. Audit before turning it on globally.
- Providers disagree on whether a filtered response carries refusal
  text, partial text, or nothing. `ContentFilterError.Message` preserves
  whatever arrived, including an empty message.
- A content-filter stop is not retried and does not trigger
  `WithFallback`; it is a terminal response, not a provider error. If a
  fallback model should see the same prompt, run the fallback explicitly
  or wrap the model.
- A content filter is a provider policy, not a security boundary of your
  application. Keep your own input and output guards for threats you own.
- The deciding design record is [ADR 0047](../adr/0047-content-filter-error.md).
