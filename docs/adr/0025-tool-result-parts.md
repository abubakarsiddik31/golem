# ADR 0025: Tool-result parts and definitive tool failures

## Status

Accepted.

## Context

Tools return text only: `Exec` yields a string that becomes the tool
message's content. A screenshot tool, a PDF extractor, or an audio
transcriber therefore cannot hand the model what it actually produced —
the application would have to smuggle media through the user channel
itself, losing the binding between content and the call that made it.
The parity pass and the roadmap's pre-v1.0 series both call for
multimodal tool results.

The providers disagree about where non-text tool output may travel, and
the disagreement is three-way:

- **Anthropic** carries text, image, and document blocks inside a
  `tool_result`'s content array, plus a native `is_error` flag.
- **Bedrock Converse** carries text, JSON, image, document, and video
  blocks inside a `toolResult` (per model family), plus a native
  `status: "error"`. It has no audio block.
- **OpenAI Chat Completions** (and Azure OpenAI) accept only text in a
  `role: "tool"` message, and **Gemini**'s `functionResponse` takes only
  a JSON object: both need media framed onto the user channel, the same
  channel a person uploads on, or the model cannot see it at all.

Two upstream designs inform the shape. Pydantic AI returns rich content
from tools and places it — inside the tool result where the API allows,
otherwise framed on the user channel with per-call tags applied at
request build and never stored in history — and separates a definitive
failure (`ToolFailed`, reported to the model, retry budget untouched)
from a correction request (`ModelRetry`, consumes the budget). Eino
leaves the plain string-returning interface alone and adds a parallel
"enhanced" interface returning structured parts — two execution paths
for one tool.

## Decision

One execution path, one return shape. `tool.Tool`'s `Exec` returns a
`tool.Result` — `Text` plus `Parts` (`model.Part`, validated like
prompt parts) — with `tool.Text(s)` as the mechanical migration for
text-only tools; the compiler finds every call site, so no tool
silently changes behavior.

The tool message carries the evidence. A successful execution stores
`Text` and `Parts` on its `RoleTool` message; history validation
extends its parts rule to tool messages. A definitive failure is a
typed error — `&tool.Failed{Reason}` — that the runner converts into a
tool message flagged `Failed` (a new additive `failed` JSON field):
the model sees the failure and decides what to do next, the retry
budget is untouched, and the run continues, exactly the `ModelRetry`
counter-case. Cancellation and deadline still outrank it, and any other
error still fails the run at the tool stage.

Placement is the adapter's job, applied while the request is built and
never stored — the same rule the multimodal guide already sets for
prompt parts:

- **Anthropic** maps parts to image and document blocks inside the
  `tool_result`, sets `is_error` for a failed result, and fails before
  the request on audio or video parts.
- **Bedrock** maps parts to image, document, and video blocks inside
  the `toolResult` (family support is the model's own caveat), sets
  `status: "error"` for a failure, and fails before the request on
  audio parts.
- **OpenAI and Azure** keep the tool message text (JSON-framed
  `{"error": ...}` when failed) and emit one synthetic user message
  after the call batch's results, each part framed with
  `<tool_result tool_name="..." tool_call_id="...">` tags so the model
  can attribute media to its call even when several tools return media
  in one step.
- **Gemini** puts the text or error in the `functionResponse` object and
  appends framed media parts to the same user-role content turn.

## Consequences

The `Exec` signature change is the one pre-v1 breaking change this ADR
accepts, and it is compiler-enforced: `gofmt`-clean code that used to
build stops building until each tool wraps its string in `tool.Text`.
Event observers gain `Event.Parts` beside `Event.Result`, additive and
zero for other kinds. The synthetic user message exists only on the
wire — `Result.Messages` keeps one tool message with its parts, so a
history replayed against a different provider re-places the same
evidence under that provider's rules. There is no silent drop: an
adapter that cannot carry a part kind fails the run before the request,
consistent with the prompt-parts contract. Bounding repeated definitive
failures stays the run-level usage limits' job, as upstream notes —
`ToolCalls` counts executions, `Requests` bounds the loop.
