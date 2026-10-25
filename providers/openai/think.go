package openai

import "strings"

// The <think> tag marks reasoning that some OpenAI-compatible
// endpoints inline in the message content — DeepSeek R1 served through
// generic completion runtimes does this, while its own API uses the
// reasoning_content field. Extraction is conservative: only a think
// block at the very start of the content is reasoning, because a model
// discussing the tag mid-answer is producing content, not reasoning.

const (
	thinkOpen  = "<think>"
	thinkClose = "</think>"
	// tagHold is the longest tag suffix a fragment boundary can split.
	tagHold = len(thinkClose)
)

// splitLeadingThink extracts one leading think block from content. It
// returns the visible text, the reasoning text, and whether a block
// was found at all; without one, content passes through untouched. A
// block that never closes is all reasoning — a truncated reasoning
// phase is still reasoning.
func splitLeadingThink(content string) (text string, thinking string, found bool) {
	rest := strings.TrimLeft(content, " \t\n\r")
	if !strings.HasPrefix(rest, thinkOpen) {
		return content, "", false
	}
	rest = strings.TrimLeft(rest[len(thinkOpen):], " \t\n\r")
	end := strings.Index(rest, thinkClose)
	if end < 0 {
		return "", strings.TrimSpace(rest), true
	}
	return strings.TrimLeft(rest[end+len(thinkClose):], " \t\n\r"), strings.TrimSpace(rest[:end]), true
}

// thinkFilter streams content fragments to their destination, deciding
// from the first non-whitespace bytes whether the message opens with a
// think block. Tag boundaries may split across fragments, so the
// filter holds back a tail that could be a tag prefix and releases it
// once resolved.
type thinkFilter struct {
	state int // see the constants below
	pend  strings.Builder
	think strings.Builder // un-emitted reasoning tail
	// trimContent waits for the first content bytes after a resolved
	// closing tag, whose leading whitespace is formatting.
	trimContent bool
}

const (
	thinkStart  = iota // before the first content byte: buffering
	thinkInside        // inside the block: reasoning until </think>
	thinkText          // past the decision: content forever
)

// feed takes one content fragment and returns the content and thinking
// text it resolves to; either may be empty while a decision pends.
// Fragments are raw: only the bytes that follow a resolved tag are
// trimmed once, at the transition.
func (f *thinkFilter) feed(fragment string) (content string, thinking string) {
	switch f.state {
	case thinkText:
		if f.trimContent {
			trimmed := strings.TrimLeft(fragment, " \t\n\r")
			if trimmed != "" {
				f.trimContent = false
			}
			return trimmed, ""
		}
		return fragment, ""
	case thinkInside:
		return f.cutClose(fragment)
	default: // thinkStart
		f.pend.WriteString(fragment)
		pending := f.pend.String()
		trimmed := strings.TrimLeft(pending, " \t\n\r")
		if trimmed == "" {
			return "", "" // still only whitespace; keep buffering
		}
		if strings.HasPrefix(thinkOpen, trimmed) && len(trimmed) < len(thinkOpen) {
			return "", "" // could still become the open tag
		}
		if strings.HasPrefix(trimmed, thinkOpen) {
			f.state = thinkInside
			f.pend.Reset()
			return f.cutClose(strings.TrimLeft(trimmed[len(thinkOpen):], " \t\n\r"))
		}
		// Not a think block: everything buffered is content.
		f.state = thinkText
		return pending, ""
	}
}

// cutClose scans inside-the-block bytes for the closing tag, holding
// back a tail that could be its prefix and emitting the safe prefix as
// reasoning now. On the closing tag it returns the remaining reasoning
// and the start of the visible text, trimmed once — the tag transition
// is where leading whitespace stops being reasoning formatting.
func (f *thinkFilter) cutClose(fragment string) (content string, thinking string) {
	f.think.WriteString(fragment)
	accumulated := f.think.String()
	if end := strings.Index(accumulated, thinkClose); end >= 0 {
		f.state = thinkText
		f.trimContent = true
		thinking = strings.TrimSpace(accumulated[:end])
		content = strings.TrimLeft(accumulated[end+len(thinkClose):], " \t\n\r")
		f.think.Reset()
		return content, thinking
	}
	hold := tagHold
	if len(accumulated) < hold {
		hold = len(accumulated)
	}
	// Keep the longest tail that could still grow into the closing
	// tag, that is, one that is a prefix of it.
	for hold > 0 && !strings.HasPrefix(thinkClose, accumulated[len(accumulated)-hold:]) {
		hold--
	}
	safe := accumulated[:len(accumulated)-hold]
	f.think.Reset()
	f.think.WriteString(accumulated[len(accumulated)-hold:])
	return "", safe
}

// finish flushes what the stream ended with: an unclosed block is all
// reasoning, an undecided buffer is content.
func (f *thinkFilter) finish() (content string, thinking string) {
	pending := f.pend.String()
	f.pend.Reset()
	switch f.state {
	case thinkInside:
		return "", strings.TrimSpace(f.think.String())
	case thinkStart:
		return pending, ""
	default:
		return "", ""
	}
}
