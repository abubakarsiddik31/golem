# API decision reference

## Prefer

- Required collaborators in constructors; optional behavior through typed option functions.
- Consumer-owned narrow interfaces, such as `model.Model`.
- Typed decoders at the model-to-application boundary.
- Additive capability interfaces for genuinely optional provider behavior.
- Ordered run data that can power logs and tracing without requiring either.

## Avoid

- Provider SDK types in exported core structs or method signatures.
- Configuration maps, package globals, and reflection-based registration.
- Generic interfaces where a concrete generic type is enough.
- Background work without a context, join point, and error route.
- A new exported concept that has no representative caller test.

## ADR trigger

Add an ADR when choosing a durable extension point, serialized event/trace shape, concurrency model, provider capability model, or compatibility policy.
