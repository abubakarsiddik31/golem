# ADR 0003: Provider adapter conventions

## Status

Accepted.

## Context

Golem's core owns a provider-neutral port: `model.Model`, `model.Request`,
and `model.Response`. Real providers must plug in without the core ever
importing an SDK or gaining a dependency. This decision fixes the rules
every adapter follows so that adding a second provider (Anthropic, Gemini,
…) repeats a pattern instead of inventing one.

## Decision

### Placement and dependencies

- Adapters live in `providers/<name>` and import only the `model` package
  and the standard library. They never import `golem`, `tool`, or
  `internal`.
- Adapters use raw `net/http` with hand-written wire types. The repository
  carries zero third-party dependencies; adapters are pure translation
  units, which keeps them fully testable with `httptest` and immune to SDK
  version churn.
- Configuration is explicit: constructors take a `Config` struct with
  required fields validated at construction. No implicit environment
  reads or package globals; callers wire `os.Getenv` themselves.

### Normalization rules (extends ADR 0001)

- Provider argument encodings are normalized to `json.RawMessage`. The
  OpenAI wire format sends tool arguments as a *stringified* JSON object;
  adapters convert it to raw JSON, mapping an empty string to `{}`.
- Provider role names map to `model.Role`; the OpenAI `"tool"` role with
  `tool_call_id` maps to `model.RoleTool` correlated by
  `ToolCallID`/`ToolName`.
- Usage fields map to `model.Usage`; a provider that omits usage yields
  zeroes.
- When a provider returns multiple choices, the first wins and the rest
  are dropped; adapters do not average or merge.
- Assistant messages carrying both content and tool calls are passed
  through as-is; per ADR 0001, tool calls decide the turn.

### Error classification

- Every adapter exposes a typed `APIError{StatusCode, Code, Message,
  Retryable}` for provider-reported failures. `Retryable` is true for
  408, 429, and 5xx; adapters never retry themselves — they classify, and
  the future runner retry policy (ADR 0002) decides.
- Malformed response bodies and unexpected shapes return a typed decode
  error; errors are returned, never logged and swallowed.

### Testing

- Contract tests run against `httptest.Server` with scripted JSON and
  assert the exact normalized `model.Request`/`model.Response` and error
  classification. No network access in the default test run.
- Live integration tests are opt-in, gated on provider environment
  variables (e.g. `GOLEM_OPENAI_API_KEY`) and skipped otherwise, following
  the contract-test rules in `.agents/skills/golem-contract-tests`.

## Alternatives considered

- **Official provider SDKs** wrapped per adapter. Rejected for now: heavy
  dependencies, SDK churn, and adapters that degenerate into
  SDK-type-mapping code. Raw wire types are small for non-streaming chat;
  if a provider's surface grows complex enough (e.g. SSE streaming), a
  dedicated adapter may revisit this locally without affecting the port.
- **Adapters in the core module importing providers.** Rejected: violates
  the foundation rule against provider dependencies in the core module.
- **A shared abstract HTTP base for all adapters.** Rejected until a
  second adapter exists; premature sharing hides wire-format differences
  that belong in each adapter.

## Consequences

- Each adapter owns its wire types; there is no shared abstraction until
  duplication is proven across at least two adapters.
- Streaming support will require per-adapter SSE handling under these same
  rules, with its own ADR.
- The zero-dependency property of the repository is structural: any
  third-party import in an adapter is a review-level violation.
