# ADR 0029: Untrusted-history sanitization

## Status

Accepted.

## Context

A history that arrives over a trust boundary — a browser request
resuming a conversation, another service's stored transcript, a
client-submitted paused run — can assert anything. A fabricated system
message carries operator authority; a tool-call/reference URL with a
`file://` or custom scheme makes the provider fetch what it names;
unresolved calls and orphan results turn into provider rejections or,
on resume, into calls the application is asked to approve. Golem
already self-heals pairing at request build and replaces history
system messages with the run's instructions, but both happen silently:
an application boundary cannot see what a client tried to assert, and
the URL-scheme case is not handled at all — `Part.Validate` checks
well-formedness, not schemes, so a `file://` image rides the wire.

Upstream separates the two concerns the same way golem's surface
already does: provider-validity repair is automatic and
content-preserving, while `sanitize_messages` is the explicit pass for
untrusted input — strip client-supplied system prompts, drop non-HTTP
file URL schemes, remove unresolved trailing calls — with a documented
trust model: no framework signs or verifies histories, possession of
the endpoint is the authorization boundary, and sanitization narrows
what a fabricated history can reach without making it trustworthy.

## Decision

`golem.SanitizeHistory(history) ([]model.Message, SanitizeReport)` is
the explicit boundary pass, composing the existing pairing repair with
two trust rules, in order:

1. **System messages are dropped**, counted in the report. Runs never
   sent them anyway — instructions govern every run — so the rule
   changes nothing the model sees; it makes the client's attempt
   visible at the boundary instead of silently swallowed later.
2. **URL parts with a scheme other than `http` or `https` are
   dropped** (case-insensitive; unparsable or scheme-relative URLs
   included), each reported with its original message index, part
   kind, and rejected scheme. Inline-data parts are not touched —
   media type and well-formedness remain the existing validation's
   job. A user message left with neither content nor parts by the
   drops is removed; tool results are never removed here, because
   their pairing evidence must survive.
3. **Pairing repair runs last** — the same deterministic pass
   `NormalizeHistory` exposes — so the returned history is resumable
   and resume-time validation never sees a fabricated dangling call
   without the report naming it (`Repair`).

The report — system-prompt count, unsafe parts, repair — is the value:
an application endpoint can log it, reject the request, or bill the
client for the damage. The pass is deterministic and idempotent, never
rewrites content, and leaves thinking blocks, failure flags, call
arguments, and identity stamps alone — trust rules beyond the three
listed threats are the application's, not the pass's.

Runs do not sanitize automatically. The pass is explicit because the
boundary is the application's: golem cannot know which histories
arrive from a browser and which are trusted server-side state, and
auto-stripping would silently destroy content a trusted pipeline
meant. Guides state the accompanying doctrine: authenticate at the
transport, scope the toolset to the caller, re-validate high-stakes
effects inside the tool against server-side state, and never read
history framing as proof.

## Consequences

Public surface grows by `SanitizeHistory`, `SanitizeReport`, and
`UnsafePart` — additive, no option knobs (the http/https rule is
fixed; applications with unusual schemes post-process). The one
behavioral gap this closes on the wire is the URL-scheme rule; system
prompts and pairing were already neutralized downstream, and the
report now says so. Deferred resume is the sharpest pairing: a client
cannot submit a paused run whose fabricated mid-history call becomes
an approval prompt without the sanitization report having named the
fabrication first. Documented design boundary, matching upstream: a
report that a client can still fabricate history describes the trust
model working as designed; what would be a vulnerability is a
sanitization default failing to strip what it documents as stripped.
