package openai

import (
	"strings"
	"testing"
)

// TestSplitLeadingThink covers the extraction rules directly: one
// leading block, trimmed; no block, untouched; a mid-content tag is
// content; an unclosed block is all reasoning.
func TestSplitLeadingThink(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		content      string
		wantText     string
		wantThinking string
		wantFound    bool
	}{
		{
			name:         "no tag",
			content:      "Just an answer.",
			wantText:     "Just an answer.",
			wantThinking: "",
			wantFound:    false,
		},
		{
			name:         "leading block",
			content:      "<think>\nLet me work it out.\n</think>\n\nThe answer is 4.",
			wantText:     "The answer is 4.",
			wantThinking: "Let me work it out.",
			wantFound:    true,
		},
		{
			name:         "whitespace before the tag",
			content:      "\n\n<think>reasoning</think>answer",
			wantText:     "answer",
			wantThinking: "reasoning",
			wantFound:    true,
		},
		{
			name:         "unclosed block is all reasoning",
			content:      "<think>truncated mid-thought",
			wantText:     "",
			wantThinking: "truncated mid-thought",
			wantFound:    true,
		},
		{
			name:         "a mid-content tag is literal content",
			content:      "The model may emit <think> tags; they are not reasoning there.",
			wantText:     "The model may emit <think> tags; they are not reasoning there.",
			wantThinking: "",
			wantFound:    false,
		},
		{
			name:         "empty reasoning",
			content:      "<think></think>done",
			wantText:     "done",
			wantThinking: "",
			wantFound:    true,
		},
		{
			name:         "empty content",
			content:      "",
			wantText:     "",
			wantThinking: "",
			wantFound:    false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			text, thinking, found := splitLeadingThink(test.content)
			if text != test.wantText || thinking != test.wantThinking || found != test.wantFound {
				t.Errorf("splitLeadingThink(%q) = (%q, %q, %v), want (%q, %q, %v)",
					test.content, text, thinking, found, test.wantText, test.wantThinking, test.wantFound)
			}
		})
	}
}

// TestThinkFilterSplitTags feeds the filter fragment by fragment and
// checks that held-back tag bytes resolve in the right channel.
func TestThinkFilterSplitTags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		fragments    []string
		wantContent  string
		wantThinking string
	}{
		{
			name:         "open tag split mid-name",
			fragments:    []string{"<thi", "nk>hidden</th", "ink>visible"},
			wantContent:  "visible",
			wantThinking: "hidden",
		},
		{
			name:         "close tag split mid-name",
			fragments:    []string{"<think>step one </step two", "</think>", " done"},
			wantContent:  "done",
			wantThinking: "step one </step two",
		},
		{
			name:         "leading whitespace then tag",
			fragments:    []string{"  ", "\n<think>", "r", "</think>a"},
			wantContent:  "a",
			wantThinking: "r",
		},
		{
			name:         "not a tag",
			fragments:    []string{"<t", "h>", "plain"},
			wantContent:  "<th>plain",
			wantThinking: "",
		},
		{
			name:         "tag with no reasoning and no close",
			fragments:    []string{"<think>"},
			wantContent:  "",
			wantThinking: "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var filter thinkFilter
			var content, thinking strings.Builder
			for _, fragment := range test.fragments {
				c, th := filter.feed(fragment)
				content.WriteString(c)
				thinking.WriteString(th)
			}
			tailContent, tailThinking := filter.finish()
			content.WriteString(tailContent)
			thinking.WriteString(tailThinking)
			if content.String() != test.wantContent || thinking.String() != test.wantThinking {
				t.Errorf("filter(%#v) = (%q, %q), want (%q, %q)",
					test.fragments, content.String(), thinking.String(), test.wantContent, test.wantThinking)
			}
		})
	}
}
