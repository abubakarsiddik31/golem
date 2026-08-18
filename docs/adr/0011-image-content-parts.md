# ADR 0011: Image content parts

## Status

Accepted.

## Context

ADR 0001 gave `model.Message` a single `Content string` and explicitly
deferred a parts-list message until a real feature forced the question.
Multimodal input is that feature: vision-capable models expect images
alongside the prompt text, and every adapter must express the same intent
in a different wire shape.

The message JSON is already a durable, additive-only contract for persisted
conversations (`Result.Messages`), so any change must leave existing
encodings byte-identical and decode history written before the change.

## Decision

Extend `model.Message` additively with `Parts []Part`. Text stays in
`Content`; parts are non-text content appended after it, carried only on
user messages.

`Part` is a flat struct: `Kind` (`"image"` only for now), `URL` xor `Data`
(inline bytes, base64 in JSON) plus the `MediaType` that `Data` requires.
Constructors `model.ImageURL(url)` and `model.ImageData(mediaType, data)`
make the common path well-formed by construction; `Part.Validate()` is the
boundary check the agent runs at run start, which also covers parts that
arrive through decoded history rather than constructors.

The agent attaches parts to a run's prompt message through variadic
`RunOption`s (`WithPromptParts`, `WithPromptImageURL`,
`WithPromptImageData`) on the existing run methods, so current call sites
compile unchanged and no new run-method family is added.

Adapters translate user-turn parts to their native multimodal form:

- **openai / azure:** chat-completions content-part arrays; inline data
  becomes a `data:<mediaType>;base64,` URL.
- **anthropic:** `image` blocks with a `url` or `base64` source.
- **gemini:** `inlineData` for bytes; `fileData` for URLs, which must be
  Gemini-accessible (Files API or GCS) — inline data is the portable form.
- **bedrock:** Converse `image` blocks with inline bytes only. A URL part
  fails with a typed adapter error before any request is sent; the adapter
  never silently drops content.

## Alternatives considered

- **Rewrite `Message` as a parts list** (text as one part among many, as
  Pydantic AI models it). Most general, but it breaks the pinned additive
  JSON encoding, every call site, and the common text-only path — the same
  reasons ADR 0001 rejected it.
- **A sealed `Part` interface with per-kind types.** The interface cannot
  enforce the URL-xor-Data invariant any better than constructors do, and
  extending it with a new kind (audio, documents) later means new types
  plus decoding hooks instead of one new `PartKind`.

## Consequences

- Persisted history gains a `parts` key only where images exist; older
  history decodes unchanged and text-only messages encode byte-identically
  (pinned by tests).
- Assistant output stays text-only; multimodal generation is out of scope
  until a feature asks for it.
- Tool results remain text; image-bearing tool results would revisit this
  ADR.
- Bedrock users holding images by URL must fetch and attach inline data;
  the typed error says so.
