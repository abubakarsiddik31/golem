# Golem implementation patterns

## Typed boundary

Use type parameters for values owned by the application, then decode untrusted provider output explicitly.

```go
type OutputDecoder[Output any] interface {
    Decode(context.Context, model.Response) (Output, error)
}
```

Do not expose provider-specific response objects from the core public result.

## Functional configuration

Add optional configuration through option functions; keep required dependencies as constructor parameters.

```go
func New[D, O any](m model.Model, decoder OutputDecoder[O], opts ...Option[D, O]) (*Agent[D, O], error)
```

Avoid configuration maps and package-level defaults. They hide validation and make tests interfere with one another.

## Narrow ports

Own interfaces at the consumer boundary. A model adapter implements `model.Model`; the core does not know its SDK, endpoint, or credentials.

Prefer a small capability extension interface when a feature is genuinely optional instead of growing the base interface for every provider.

## Run evidence

Record normalized messages, usage, tool calls, and terminal cause in an ordered run result. A tracing exporter may observe this evidence; tracing must not be required to use the agent.

## Errors and retries

Return an error that preserves its cause and includes the stage. Keep retryability as explicit policy evaluated by the runner; never make an adapter silently retry a non-idempotent operation.

## Tools

Represent a tool's name, description, input schema, and execution separately. Describe it without invoking it. Execute it with `context.Context` and a typed `RunContext[D]`. Treat tool output as a new normalized message for the model loop.

## Tests

Use fakes that capture requests and return deterministic responses. Test request construction, result evidence, source-error preservation, and context cancellation. Keep provider integration tests in adapter packages and opt-in through environment configuration.
