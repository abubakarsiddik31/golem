# ADR 0020: Finish reason visibility

## Status

Accepted.

## Context

Every provider reports why a model turn ended — a wire field the
adapters decoded but discarded: `finish_reason` on OpenAI- and
Azure-compatible chat completions (never parsed), `stop_reason` on
Anthropic Messages (never parsed), `stopReason` on Bedrock Converse
(decoded, never read), and `finishReason` on Gemini GenerateContent
(used only as the stream's terminal sentinel). A run therefore cannot
see its own terminal cause: a response truncated by the output token
cap is indistinguishable from a complete one, except on Gemini streams
where the missing sentinel fails the run. Truncated JSON that the
decoder rejects — the most common structured-output failure — reports
only "the output was invalid", with the provider's own explanation
dropped.

The vocabularies are provider-specific but semantically parallel:
OpenAI's `stop`/`length`/`tool_calls`/`content_filter` (plus legacy
`function_call`), Anthropic's
`end_turn`/`max_tokens`/`stop_sequence`/`tool_use`/`refusal`/`pause_turn`,
Bedrock's `end_turn`/`max_tokens`/`stop_sequence`/`tool_use`/`guardrail_intervened`/`content_filtered`,
and Gemini's `STOP`/`MAX_TOKENS`/`SAFETY`/`RECITATION`/`BLOCKLIST`/`PROHIBITED_CONTENT`/`SPII`/`MALFORMED_FUNCTION_CALL`.

## Decision

`model.Response` carries `FinishReason model.FinishReason` — a
normalized string vocabulary of five constants, learned from upstream's
finish-reason handling but Go-native:

- `FinishStop` — natural completion, including a hit stop sequence.
- `FinishLength` — the output token or length cap truncated the turn.
- `FinishToolCall` — the turn ended on tool requests.
- `FinishContentFilter` — a provider safety system ended the turn;
  the response may still carry refusal or partial text.
- `FinishOther` — a terminal cause outside the vocabulary, so an
  undocumented provider value stays visible as "not stop".

The zero value means the provider reported nothing; hand-built fakes
leave it empty by design. Each adapter pins its translation table in a
documented helper, and unknown wire values map to `FinishOther` rather
than failing: finish reasons are evidence, not validation. A raw-string
passthrough was rejected — applications would re-learn every provider's
vocabulary — and a parallel "provider details" bag was rejected as
surface beyond any demonstrated need.

The cause threads through the evidence chain: the runner keeps the last
completed turn's reason on its outcome, so `Result.FinishReason` holds
the final turn's cause on success and pause, and
`RunError.Partial.FinishReason` holds the last completed turn's cause on
failure. Last-wins across output correction rounds, matching how usage
and activity counts already accumulate.

## Consequences

Truncation becomes diagnosable everywhere: a decode failure whose
`Partial.FinishReason` is `FinishLength` names its cause. Content-filter
responses that carry refusal text surface as ordinary results with the
cause visible, matching the core's evidence-preserving posture; forcing
them to be errors remains an application policy (the decoder, as ever,
is the validation boundary). Each new provider value requires an
adapter mapping decision — the tables are contract, pinned by tests —
but unrecognized values degrade to `FinishOther`, never to a failure.
