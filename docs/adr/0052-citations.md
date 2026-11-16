# ADR 0052: Provider citations as durable message metadata

Date: 2026-11-16
Status: Accepted

## Context

Pydantic AI puts citations on text parts in one provider-neutral shape:
sources, excerpts, and an anchor into the answer. Golem's message model
has one text `Content` string per message, not text parts, and its
adapters are standard-library only. Provider citation support also
depends on the provider's native tools or document features; Golem has
no native provider-executed tools, so a full cross-provider citation
layer would be mostly empty surface.

Anthropic can cite documents directly: a document block can request
citations, and text blocks return citation objects with excerpts,
titles, character offsets, and page numbers. That is a real source of
citations inside the existing adapter and needs no native tool.

## Decision

Add `model.Citation` and the additive `model.Message.Citations` field.

```go
type Citation struct {
    Type, Text, Title, URL string
    StartCharIndex, EndCharIndex, PageNumber int
}
```

`Type` is the provider's location kind. Fields are filled only when the
provider supplies them; the answer text is never rewritten, and citation
anchors into the answer are not synthesized. The JSON tags are durable
and additive.

`anthropic.Config.Citations` opts in: document blocks request citations,
and plain and streamed responses parse provider citation arrays or
`citations_delta` events onto the final assistant message. It is off by
default because it changes request shape and is provider/model
dependent. Invalid citation payloads are decode errors, not silently
dropped evidence.

Other adapters leave `Citations` empty in v1. The contract is
provider-neutral, so Bedrock, Gemini grounding, or OpenAI annotations
can populate it additively when their citation sources are supported.

## Alternatives considered

- **Per-text-part citations** (Pydantic's shape): Golem messages carry a
  single content string, and introducing parts for assistant text now
  would reshape a frozen durable contract. Message-level metadata is the
  additive step.
- **Invent answer anchors**: Anthropic does not report where in the
  answer a citation applies. Fabricating offsets would claim more than
  the provider said.
- **Enable citations by default**: changes request shape and is
  unsupported on some models and proxies.
- **Bedrock, Gemini, and OpenAI in the same PR**: each needs a citation
  source (documents, grounding tools, or annotations) and its own wire
  parsing. The contract lands now; adapters follow on evidence.
- **A citation tool or RAG package**: application-level bracket
  citations already exist in products such as Herbie. This decision is
  about provider-reported source metadata, not a retrieval scheme.

## Consequences

- New exported names: `model.Citation`, `Message.Citations`, and
  `anthropic.Config.Citations`; all additive.
- Anthropic document requests change only when opted in. Streamed and
  plain responses share the same normalized citation fields.
- Citations are provider metadata, not verified truth: excerpts may
  quote documents, and applications must treat them as untrusted.
- No answer-anchor mapping in v1; UIs can show sources and excerpts, not
  highlight the exact answer span.
- Tests cover request opt-in, plain parsing, streamed `citations_delta`,
  malformed payloads, and the disabled default.
