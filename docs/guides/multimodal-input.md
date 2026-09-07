# Multimodal input

## Purpose

Attach non-text content — images, documents, audio, video — to a run's
prompt so multimodal models can see, read, or hear it: `model.Part`
values on the prompt message, with helpers for the common cases.

## When to use

When a prompt needs more than text: describing a photo, reading a chart,
summarizing a PDF, transcribing a recording, reviewing a clip. Not for
plain text (send it as the prompt), and not for tool results — they are
text (a media-bearing tool result would need a contract change; none
exists yet).

## How it works

Parts attach to the final user message of a run, after the prompt text,
through run options accepted by `Run`, `RunWithHistory`, `RunStream`, and
`RunStreamWithHistory`:

```go
result, err := agent.Run(ctx, runCtx, "describe this",
	golem.WithPromptImageURL("https://example.com/photo.png"))
```

Options are evaluated once at run start. Every part is validated
(`model.Part.Validate`) before any model call: a malformed part, or parts
on a history message other than a user message, fails the run up front
with a plain error — no provider request is made.

Parts ride on `model.Message.Parts` in the normalized conversation, so
they appear in `Result.Messages` and persist with the same additive-only
JSON contract as every other message field: history written before this
feature exists decodes unchanged, and text-only messages encode
byte-identically. Inline data is base64 in JSON. To read a part back
from evidence, switch on its kind — `model.PartImage`, `PartDocument`,
`PartAudio`, or `PartVideo`:

```go
for _, part := range message.Parts {
    if part.Kind == model.PartDocument {
        fmt.Println(part.MediaType, part.URL, len(part.Data))
    }
}
```

A part carries exactly one of a URL (the provider fetches the content)
or inline `Data` plus its `MediaType`. Every adapter translates the
kinds its provider accepts to the native wire form and rejects the rest
before any request is sent, with an error that names the unsupported
combination and the portable alternative:

| Adapter | Image | Document | Audio | Video |
| --- | --- | --- | --- | --- |
| openai, azure | URL or data URL, any image type | inline `application/pdf` only | inline `audio/wav` or `audio/mpeg` | rejected |
| anthropic | URL or base64, provider's image types | URL or inline `application/pdf` | rejected | rejected |
| gemini | URL (Files API/GCS) or inline | URL or inline, `application/pdf` and text types | inline, or URL the provider reaches | inline, or URL (incl. YouTube) |
| bedrock | inline png/jpeg/gif/webp | inline pdf, csv, doc(x), xls(x), html, txt, md | rejected | rejected |

Rejections wrap the adapter's typed error — `DecodeError` on the
chat-completions and Messages adapters, `ErrUnsupportedContent` on
bedrock — and media types an endpoint cannot carry fail the same way,
so a misrouted part never reaches the provider as a cryptic 400.
Inline data is the portable form; URLs only where the provider fetches
content itself.

The bedrock document block requires a name, which the adapter derives
neutrally (`document-1`, `document-2`, …) — AWS flags the field as
prompt-injection-prone and recommends against customer-controlled
values.

## Example

`examples/multimodal-input` embeds a 1×1 red PNG and a small PDF, and
asks the model to describe both; set `OPENAI_API_KEY` (and optionally
`OPENAI_MODEL`) to run it.

```go
result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{},
	"Describe the image and summarize the document.",
	golem.WithPromptParts(
		model.ImageData("image/png", pixels),
		model.DocumentData("application/pdf", pdf),
	))
if err != nil {
	fmt.Println("Run:", err)
	return
}
fmt.Println(result.Output)
```

## API surface

- `golem.WithPromptParts(parts ...model.Part) golem.RunOption` — append validated parts to the prompt message.
- `golem.WithPromptImageURL(url string) golem.RunOption` — one image the provider fetches.
- `golem.WithPromptImageData(mediaType string, data []byte) golem.RunOption` — one inline image.
- `model.ImageURL(url string) model.Part` / `model.ImageData(mediaType string, data []byte) model.Part` — image parts.
- `model.DocumentURL(url string) model.Part` / `model.DocumentData(mediaType string, data []byte) model.Part` — document parts.
- `model.AudioData(mediaType string, data []byte) model.Part` — an inline audio part.
- `model.VideoData(mediaType string, data []byte) model.Part` — an inline video part.
- `model.Part.Validate() error` — the boundary check; runs automatically at run start.
- `model.Message.Parts []model.Part` — where parts live in normalized evidence.

## Gotchas

- Parts are valid only on user messages; the run rejects anything else,
  including assistant messages in supplied history.
- Exactly one of a part's URL or Data is set — both, or neither, is a
  validation error — and inline data requires its media type. Audio and
  video URLs exist only on gemini (provider-addressable URIs); build
  them as `model.Part{Kind: model.PartAudio, URL: ...}`.
- Data is application-owned and not copied: treat the byte slice as
  immutable once attached (recorded evidence in `testmodel` is copied).
- Inline data rides the request body as base64 — large media mean large
  payloads and token counts; providers bill for image and document
  tokens. Gemini caps a request's inline payload (about 20 MB total);
  larger content belongs in Files API or GCS objects behind URLs.
- Document kinds are stricter than images on several adapters: the
  chat-completions adapters accept inline PDFs only, anthropic pairs its
  PDF rule with URL support, and bedrock wants an explicit
  media-type-to-format match. The openai-compatible table also depends
  on the endpoint behind `BaseURL` — local runtimes may accept none of
  the media parts.
- Deciding contracts: `docs/adr/0011-image-content-parts.md` and
  `docs/adr/0022-media-input-parts.md`.
