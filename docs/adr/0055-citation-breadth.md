# ADR 0055: Citation breadth across adapters

Date: 2026-11-16
Status: Accepted

## Context

ADR 0052 added the provider-neutral `model.Citation` contract and the
first implementation: Anthropic document citations behind
`anthropic.Config.Citations`. The contract was designed to be filled by
other adapters without changing `model.Message`.

Each remaining adapter has a different citation source:

- Bedrock Converse returns document citations in `citationsContent`
  blocks and accepts a document-level `citations` request setting.
- Gemini returns `groundingMetadata` with chunks and supported answer
  segments when a grounding/search tool is enabled.
- OpenAI-compatible chat endpoints return `annotations` with
  `url_citation` entries when web search is enabled.

Golem does not provide native provider-executed search tools, so for
Gemini and OpenAI the source is the endpoint or a proxy configuration,
not a Golem request feature.

## Decision

Extend the existing contract:

- **Bedrock** adds `Config.Citations`, sets `citations.enabled` on
  document blocks, and parses `citationsContent` text and citation
  locations into `Message.Citations`.
- **Gemini** parses `groundingMetadata` on plain and streamed
  candidates: each support segment becomes a citation for every chunk it
  names, with URI, title, segment text, and start/end offsets.
- **OpenAI-compatible** adapters parse `annotations[].url_citation` from
  plain messages and streamed deltas into the same fields.

Citations carry the provider's type, excerpt, title, URL, character
offsets, and page number when supplied. No answer anchors are invented.
For Gemini and OpenAI, request-side enabling remains the application's
concern; the adapter only preserves citations an endpoint returns.

## Alternatives considered

- **One shared source union type**: providers disagree on location
  semantics; the flat, optional-field citation stays additive and
  durable.
- **Enable Gemini Google Search or OpenAI web search in the adapters**:
  that is a native-tool feature, explicitly demand-gated in the roadmap,
  and would change request shape and billing.
- **Defer non-Anthropic adapters**: leaves real Bedrock document
  citations unreachable and ignores citations compatible endpoints
  already return.
- **Per-provider citation types**: leaks provider vocabulary into the
  durable message contract and forces every UI to branch.

## Consequences

- New exported field: `bedrock.Config.Citations`; Gemini and OpenAI need
  no new configuration and only populate when the provider returns
  metadata.
- Bedrock request shape changes only when opted in. Streaming Bedrock
  citation events are not parsed yet; plain responses are. The guide
  states the current boundary.
- Gemini and OpenAI citation metadata is only as good as the endpoint's
  configuration; the guide says the application must enable the search
  source.
- Tests cover Bedrock request opt-in and parsing, Gemini plain and
  streamed grounding, and OpenAI plain and streamed annotations.
