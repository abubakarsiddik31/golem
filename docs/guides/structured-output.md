# Structured output

## Purpose

Tell the model the expected answer shape up front and decode the response
into a typed value — requested and validated with one declaration.

## When to use

Whenever the output is data, not prose: extraction, classification,
structured reports. Use plain decoding when free text is genuinely the
product.

## How it works

`WithOutputSchema` declares a JSON Schema for the final answer. Adapters
map it to their native mechanism — OpenAI `response_format`
(`json_schema`, strict), Anthropic `output_config` — and adapters without
structured-output support ignore it. The schema describes the shape to
the model; the decoder remains the validation boundary, so responses are
still validated the same way regardless of provider.

`golem.DecodeJSON[Output]()` returns a decoder that unmarshals the
response content as JSON into `Output` and rejects malformed content with
`model.ModelRetry`, so an enabled correction budget asks the model to fix
the response instead of failing.

## Example

Run `examples/structured-output`:

```bash
OPENAI_API_KEY=sk-... go run ./examples/structured-output
```

```go
agent, _ := golem.New[struct{}, City](client, golem.DecodeJSON[City](),
    golem.WithOutputSchema[struct{}, City](json.RawMessage(`{
        "type": "object",
        "properties": {"city": {"type": "string"}},
        "required": ["city"],
        "additionalProperties": false
    }`)),
)
```

## API surface

- `golem.WithOutputSchema[Deps, Output](schema json.RawMessage)`
- `golem.DecodeJSON[Output]() OutputDecoder[Output]`
- `model.Request.OutputSchema` — the provider-neutral request field.

## Gotchas

- Both shipped providers enforce strict schemas: `additionalProperties:
  false` and every property listed in `required`. Non-conformant schemas
  surface as provider `APIError`s, not silent relaxation.
- An empty schema disables the behavior; invalid JSON fails construction.
- The schema is decoupled from the decoder: applications can validate
  more strictly than the schema they advertise.
- Decisions live in `docs/adr/0010-output-schema.md`.
