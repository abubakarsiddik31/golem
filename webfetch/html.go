package webfetch

import (
	"html"
	"strings"
)

// htmlToText reduces an HTML document to its visible text: comments,
// script and style contents, and all markup are dropped; character
// entities are unescaped; block-level tags separate lines. It is a
// best-effort scan, not a rendering engine — the contract is the tool
// boundary, not pixel-accurate output.
func htmlToText(body string) string {
	var out strings.Builder
	i := 0
	for i < len(body) {
		lt := strings.IndexByte(body[i:], '<')
		if lt < 0 {
			writeText(&out, body[i:])
			break
		}
		writeText(&out, body[i:i+lt])
		i += lt + 1 // position just past '<'

		if strings.HasPrefix(body[i:], "!--") {
			end := strings.Index(body[i+3:], "-->")
			if end < 0 {
				break
			}
			i += 3 + end + 3
			continue
		}

		name, after, ok := readTagName(body, i)
		if !ok {
			break
		}
		if name == "script" || name == "style" {
			closeIdx := indexCloseTag(body[after:], name)
			if closeIdx < 0 {
				break
			}
			from := after + closeIdx
			gt := strings.IndexByte(body[from:], '>')
			if gt < 0 {
				break
			}
			i = from + gt + 1
			continue
		}
		end := skipTag(body, i)
		if end < 0 {
			break
		}
		i = end
		if blockTags[name] {
			out.WriteByte('\n')
		}
	}
	return normalizeLines(out.String())
}

// writeText appends one text run with its character entities unescaped.
// Runs end at a literal '<', which no entity reference contains, so
// unescaping per run cannot split an entity.
func writeText(out *strings.Builder, text string) {
	if text == "" {
		return
	}
	out.WriteString(html.UnescapeString(text))
}

// readTagName reads the tag name starting at i — an optional '/' for
// closing tags included — and returns it lowercased with the position
// after the name.
func readTagName(body string, i int) (name string, after int, ok bool) {
	if i < len(body) && body[i] == '/' {
		i++
	}
	start := i
	for i < len(body) && isTagNameByte(body[i]) {
		i++
	}
	if i == start {
		return "", i, false
	}
	return strings.ToLower(body[start:i]), i, true
}

// isTagNameByte accepts the bytes of an HTML tag name: letters, digits,
// and the markup sigils that introduce doctypes and other special
// forms, so `<!DOCTYPE html>` scans as one tag instead of text.
func isTagNameByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == '!' || c == '-':
		return true
	}
	return false
}

// skipTag returns the position after the '>' closing the tag that starts
// at i, honoring quoted attribute values so '>' inside quotes does not
// end the tag. It returns -1 for an unterminated tag.
func skipTag(body string, i int) int {
	var quote byte
	for ; i < len(body); i++ {
		c := body[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '>':
			return i + 1
		}
	}
	return -1
}

// indexCloseTag returns the offset of body's first "</name" (the closing
// tag of a raw-text element), case-insensitively, or -1.
func indexCloseTag(body string, name string) int {
	prefix := "</" + name
	for i := 0; i+len(prefix) <= len(body); i++ {
		if strings.EqualFold(body[i:i+len(prefix)], prefix) {
			return i
		}
	}
	return -1
}

// blockTags are the elements whose boundaries separate output lines;
// tags not listed here render their contents inline.
var blockTags = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true,
	"br": true, "caption": true, "dd": true, "details": true, "div": true,
	"dl": true, "dt": true, "fieldset": true, "figcaption": true,
	"figure": true, "footer": true, "form": true, "h1": true, "h2": true,
	"h3": true, "h4": true, "h5": true, "h6": true, "header": true,
	"hr": true, "li": true, "main": true, "nav": true, "ol": true,
	"p": true, "pre": true, "section": true, "summary": true,
	"table": true, "td": true, "th": true, "title": true, "tr": true,
	"ul": true,
}

// normalizeLines trims each line, drops empties, and joins the rest with
// newlines: source indentation and the blank runs block tags produce
// collapse to single separators.
func normalizeLines(text string) string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
