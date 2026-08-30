# Multimodal input

## Purpose

Attach images to a run's prompt so vision-capable models can see them:
`golem.WithPromptImageURL` for an image the provider fetches,
`golem.WithPromptImageData` for bytes your application already holds.

## When to use

When a prompt needs to reference an image — describing a photo, reading a
chart, comparing two renders. Not for files the model cannot see: plain
text prompts stay text, and tool results are text (an image-bearing tool
result would need a contract change; none exists yet).

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
from evidence, switch on its kind:

```go
for _, part := range message.Parts {
    if part.Kind == model.PartImage {
        fmt.Println(part.MediaType, part.URL, len(part.Data))
    }
}
```

Each adapter translates parts to its provider's native form:

| Adapter | URL part | Inline data part |
| --- | --- | --- |
| openai, azure | `image_url` the provider fetches | data URL in an `image_url` part |
| anthropic | `url` source block | `base64` source block with media type |
| gemini | `fileData` URI — must be Files API or GCS, content the provider reaches itself | `inlineData` with base64 |
| bedrock | rejected: `ErrUnsupportedContent` | Converse `image` block, png/jpeg/gif/webp only |

The bedrock adapter never silently drops content: a URL part, or a media
type outside `image/png`, `image/jpeg`, `image/gif`, `image/webp`, fails
with an error wrapping `bedrock.ErrUnsupportedContent` before any request
is signed or sent.

## Example

`examples/multimodal-input` embeds a 1×1 red PNG and asks the model to
describe it; set `OPENAI_API_KEY` (and optionally `OPENAI_MODEL`) to run
it.

```go
pixels, err := base64.StdEncoding.DecodeString(redPixelPNG)
if err != nil {
	fmt.Println("decode embedded image:", err)
	return
}
result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{},
	"Describe this image in one short sentence.",
	golem.WithPromptImageData("image/png", pixels))
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
- `model.ImageURL(url string) model.Part` — construct a URL part directly.
- `model.ImageData(mediaType string, data []byte) model.Part` — construct an inline part directly.
- `model.Part.Validate() error` — the boundary check; runs automatically at run start.
- `model.Message.Parts []model.Part` — where parts live in normalized evidence.

## Gotchas

- Parts are valid only on user messages; the run rejects anything else,
  including assistant messages in supplied history.
- Exactly one of a part's URL or Data is set — both, or neither, is a
  validation error — and inline data requires its media type.
- Data is application-owned and not copied: treat the byte slice as
  immutable once attached (recorded evidence in `testmodel` is copied).
- Inline data rides the request body as base64 — large images mean large
  payloads and token counts; providers bill for image tokens.
- URL handling is provider-specific: gemini only resolves URIs it can
  reach itself (Files API or GCS), and bedrock rejects URLs outright.
  Inline data is the portable form.
- Deciding contract: `docs/adr/0011-image-content-parts.md`.
