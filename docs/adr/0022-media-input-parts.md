# ADR 0022: Document, audio, and video input parts

## Status

Accepted.

## Context

ADR 0011 gave `model.Message` a parts list with one kind — `PartImage` —
and `Part.Validate` rejects every other kind string. Retrieval
applications need more than vision: RAG pipelines hand the model PDFs,
agents record audio, and Gemini accepts video. The durable message JSON
is an additive-only contract, so new kinds must ride the existing `Part`
fields and leave old encodings byte-identical.

The providers disagree about what they accept and how. OpenAI's chat
completions take documents as `file` content parts (inline base64
`file_data`) and audio as `input_audio` parts (base64, wav or mp3 only);
there is no video input. Anthropic takes documents as `document` blocks —
base64 `application/pdf` inline or a `url` source — and accepts no audio
or video. Gemini's `inlineData`/`fileData` parts take any of it —
documents, audio, video — keyed by MIME type, with URLs resolved only
when the provider can reach them itself. Bedrock's Converse API takes
documents as `document` blocks (inline bytes, an explicit `format`
extension, and a mandatory `name` field AWS flags as
prompt-injection-prone) and accepts no audio or video.

## Decision

Extend the part vocabulary additively: `PartDocument` (`"document"`),
`PartAudio` (`"audio"`), and `PartVideo` (`"video"`) join `PartImage`,
all sharing the existing flat shape — `URL` xor `Data` plus the
`MediaType` that inline data requires. Kind carries intent, not just a
media-type prefix: it selects the wire shape where a provider treats the
same bytes differently, and it lets adapters surface a contradiction
(audio kind with a PDF media type) as an error instead of silently
routing by MIME. Constructors `model.DocumentURL`, `model.DocumentData`,
`model.AudioData`, and `model.VideoData` join the image pair; audio and
video URLs ride the struct literal directly, since only Gemini resolves
them. `Validate` accepts the new kinds under the same rules and still
rejects unknown ones. No new run options: `WithPromptParts` already
carries any validated part.

Each adapter maps the kinds it can and rejects the rest before any
request is sent, with an error that names the unsupported combination
and the portable alternative — the bedrock `ErrUnsupportedContent`
pattern generalized. Inline data is the portable form everywhere;
URLs only where the provider fetches (Anthropic PDF URLs, Gemini's
provider-addressable URIs). Derived wire values stay neutral and
documented: Bedrock document names are `document-N` (AWS recommends a
neutral name), OpenAI file parts carry a matching `filename`. Anthropic
inline documents are `application/pdf` only — its plain-text document
source stays out until demanded, since plain text belongs in message
content.

Rejected: inferring kind from media type alone (contradictions become
silent routing decisions); a separate file-name field on `Part` (no
demonstrated need; adapters derive neutral names); per-provider part
types behind the model boundary (the normalized message is the
durable contract and must stay provider-neutral); citations, document
titles, and Anthropic content-block sources (no demand yet, all
additive later).

## Consequences

A prompt can carry a PDF, a recording, or a clip with the same
validation, JSON durability, and evidence semantics as an image.
Each new provider media capability is an adapter mapping decision
pinned by tests, and unsupported combinations fail up front with an
actionable error rather than a cryptic provider 400. The vocabulary is
additive: history encoded before this change decodes unchanged, and
text-only messages still encode byte-identically.
