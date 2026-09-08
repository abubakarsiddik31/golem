# ADR 0023: Input-token counting

## Status

Accepted.

## Context

Usage limits on a run are checked against provider-reported usage after
each response, so an oversized request is still sent and billed. Budget
awareness needs the other direction: asking the provider how many tokens
a request would consume before sending it — for pre-send limits and for
history bounding by budget instead of message count. A parity pass
against Pydantic AI (whose `count_tokens` and pre-request limit checks
cover the same ground) and the roadmap's pre-v1.0 series both call for
it.

The providers disagree about whether counting exists at all. Anthropic
exposes `POST /v1/messages/count_tokens` taking the inference request's
`model`, `messages`, `system`, and `tools`, returning `input_tokens`;
the endpoint validates the body with the same schema as inference.
Gemini exposes `models/{model}:countTokens` taking the inference
request's `contents`, `systemInstruction`, and `tools`, returning
`totalTokens`. Bedrock exposes `CountTokens` on `bedrock-runtime` —
`POST /model/{modelId}/count-tokens` with an `input.converse` body of
`messages` and `system`, returning `inputTokens`; it is free of charge,
counts only what the body carries (tool definitions are not priced),
rejects inference-profile model IDs, and is unsupported on some
Anthropic-on-Bedrock models. OpenAI and Azure OpenAI expose no counting
endpoint at all; their ecosystem counts client-side with a tokenizer
library, which would put a data dependency inside the core.

## Decision

Add a `tokens` package beside `embedding`: a one-method `Counter` port
(`CountTokens(ctx, CountInput) (int, error)`) whose `CountInput` is the
would-be request — `Messages` and `Tools` — and whose result is a bare
token count. Adapters ship only where the provider offers an endpoint:
`anthropic.Counter`, `gemini.Counter`, and `bedrock.Counter`, each
constructed from the adapter's existing `Config` and reusing that
adapter's request builders so a count prices exactly what inference
would send. OpenAI and Azure get no counter; applications that need one
there implement the port against their own tokenizer, the same seam
`testmodel` uses for fakes.

On top of the port, two users in the core:

- `UsageLimit.PerRequestInputTokens` bounds one request's estimated
  input. With `WithTokenCounter` wired, the runner counts before every
  model call and fails the run — usage stage, evidence preserved —
  when the estimate crosses the bound, before the request is sent. The
  bound without a counter is a construction error, not a silently
  skipped check.
- `BudgetHistory(counter, maxTokens)` is the token-budget sibling of
  `TrimHistory`: it counts the history and, while over budget, drops
  from the oldest side under the same boundary rule TrimHistory uses,
  re-counting after each drop. Counting is an HTTP call per drop, so
  the processor makes at most one call per dropped message plus one;
  the guide says to prefer a generous budget over a tight one.

Counting counts what the provider counts: tool definitions ride the
count on Anthropic and Gemini, not on Bedrock — a Bedrock estimate is
therefore a lower bound — and no count includes a would-be response.

## Consequences

The port is additive and provider-asymmetric by design, mirroring ADR
0021's stance that adapters exist only where the provider offers the
capability. Built-in tokenizers and tokenizer libraries stay out —
approximation heuristics rot slower than price tables but still embed
model-specific data in the core; a user-supplied `tokens.Counter` is
the extension point instead. Pre-send counting costs one extra HTTP
call per model turn when configured; that is the documented price of
the guarantee.
