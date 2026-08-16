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

### Tool mode

`WithOutputTool` expresses the same intent through tool calling: the
schema becomes the parameters of a synthesized output tool offered to the
model, and the run ends on the model's first call to it. The call's
arguments reach the decoder as the final response content, so
`DecodeJSON` validates them like any other response. Tool mode works with
every model that supports tool calling — including providers and local
models without native JSON-schema output — which is why it is the default
mode in several agent frameworks.

Tool calls co-emitted with the output call are not executed; they are
closed with an interrupted result so the conversation keeps the call/result
pairing providers require. The output call itself is closed in the result
evidence after decoding: a recorded result on success, or a rejection
bound to the call when the decoder asks for correction.

```go
agent, _ := golem.New[struct{}, City](client, golem.DecodeJSON[City](),
    golem.WithOutputTool[struct{}, City]("record_city",
        "Record the final answer.", json.RawMessage(`{...}`)),
)
```

## Example

Run `examples/structured-output` (native schema mode) or
`examples/structured-output-tool` (tool mode):

```bash
OPENAI_API_KEY=sk-... go run ./examples/structured-output
OPENAI_API_KEY=sk-... go run ./examples/structured-output-tool
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
- `golem.WithOutputTool[Deps, Output](name, description string, schema json.RawMessage)`
- `golem.DecodeJSON[Output]() OutputDecoder[Output]`
- `model.Request.OutputSchema` — the provider-neutral request field.

## Gotchas

- Both shipped providers enforce strict schemas: `additionalProperties:
  false` and every property listed in `required`. Non-conformant schemas
  surface as provider `APIError`s, not silent relaxation.
- An empty schema disables the behavior; invalid JSON fails construction.
- The schema is decoupled from the decoder: applications can validate
  more strictly than the schema they advertise.
- `WithOutputSchema` and `WithOutputTool` are mutually exclusive; configure
  one structured-output mode.
- In tool mode the first output-tool call ends the run: co-emitted tool
  calls are not executed, and the output-tool name must not collide with a
  registered tool.
- Decisions live in `docs/adr/0010-output-schema.md`.
