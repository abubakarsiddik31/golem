# ADR 0010: Output schema in model requests

## Status

Accepted.

## Context

Golem validates output after the fact: the decoder rejects responses that
do not match the application's type, and correction rounds ask the model
to fix its mistakes. The model, however, is never told what shape is
expected — it infers the format from the prompt alone, so the first
response is often malformed and correction rounds spend model turns
repairing a guess. Providers with native structured output — OpenAI's
`json_schema` response format, Anthropic's tool-based extraction — cannot
be used at all, because the request contract has no place to carry the
expected shape.

## Decision

### `model.Request` grows an optional, advisory output schema

```go
type Request struct {
    Messages   []Message
    ToolSpecs  []ToolSpec
    // OutputSchema is a JSON Schema document describing the expected
    // final answer. Adapters that support structured output map it to
    // their native mechanism; adapters that do not ignore it.
    OutputSchema json.RawMessage
}
```

The field is optional data, not behavior, so it does not need a
capability interface the way streaming did: every adapter can carry it,
and ignoring it is a documented, safe degradation. `Request` is not part
of the durable `Message` encoding contract, so the field carries no JSON
tags and no stability promise.

### The decoder remains the validation boundary

The schema describes the expected shape to the model; the decoder still
decides what becomes an application value. Golem never assumes the
provider honored the schema — a hostile or sloppy provider response is
still rejected at the decode boundary, exactly as before. Schema and
decoder stay decoupled: `WithOutputSchema` declares the shape,
`OutputDecoder` validates the content, and applications that want
schema-driven decoding compose `WithOutputSchema` with `DecodeJSON`.

### Agents declare the schema at construction

`golem.WithOutputSchema(schema)` sets the schema for every request the
agent makes. `New` validates it like a tool schema: present and valid
JSON. Per-run schemas are deferred until a caller needs them; the
additive option leaves room without a breaking change.

### The OpenAI-compatible adapter maps it to `response_format`

When `OutputSchema` is set, the adapter sends
`response_format: {"type": "json_schema", "json_schema": {"name":
"output", "strict": true, "schema": ...}}`. Strict mode is the point of
the feature — guaranteed-shape output — so it is not configurable. The
consequence is deliberate: OpenAI requires strict-conformant schemas
(`additionalProperties: false`, every property in `required`), and a
non-conformant schema surfaces as a provider API error rather than being
silently relaxed.

## Alternatives considered

- **Rely on correction rounds alone.** Rejected: they repair guesses
  after the fact, spend whole model turns doing it, and cannot engage
  providers' native structured-output guarantees at all.
- **Derive the schema from `Output` via reflection.** Rejected: the
  repository forbids reflection-based discovery; JSON tags are not a
  schema language; nullability, unions, and constraints have no faithful
  mapping from Go types.
- **A `StructuredOutputModel` capability interface.** Rejected:
  capability interfaces earn their cost for behavior (streaming), not
  for optional data every adapter can carry or ignore.
- **Validating responses against the schema in core.** Rejected:
  duplicating the decoder boundary adds a second validation regime
  without a caller; the decoder already owns rejection and correction.

## Consequences

- Requests carrying a schema get provider-enforced structure where the
  provider supports it and unchanged behavior where it does not.
- Applications using strict structured output must author
  strict-conformant schemas; the adapter reports provider rejections
  through its normal error types.
- The output path stays provider-neutral: adapters that ignore the
  field still work, with correction rounds as the fallback.
- A schema-driven decoder helper (`DecodeJSON`) can now be paired with
  the schema so the shape is both requested and validated with one
  declaration.
