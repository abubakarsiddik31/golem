package skills

import (
	"fmt"
	"strings"
)

// frontmatter holds the SKILL.md fields the tool uses. Other known
// fields (license, metadata) and unknown fields are ignored.
type frontmatter struct {
	name          string
	description   string
	compatibility string
}

// parseFrontmatter splits YAML frontmatter from the Markdown body and
// extracts the fields the tool needs. It supports the YAML subset the
// Agent Skills format uses in practice: plain scalars, single- and
// double-quoted scalars, and block scalars (|, |-, >, >-) for
// multi-line values. Anchors, flow style, and tags are not YAML-
// interpreted; their literal text becomes the value. A file without a
// well-formed frontmatter block is an error.
func parseFrontmatter(content string) (frontmatter, string, error) {
	lines := splitLines(content)
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r") != "---" {
		return frontmatter{}, "", fmt.Errorf("SKILL.md must start with a --- frontmatter block")
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r") == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return frontmatter{}, "", fmt.Errorf("frontmatter is not closed by a --- line")
	}

	var fm frontmatter
	var pendingKey string
	var block []string
	blockFolded := false
	flush := func() {
		if pendingKey == "" {
			return
		}
		value := renderBlock(block, blockFolded)
		assignField(&fm, pendingKey, value)
		pendingKey = ""
		block = nil
	}
	for _, line := range lines[1:end] {
		if pendingKey != "" && isBlockLine(line) {
			block = append(block, strings.TrimRight(line, "\r"))
			continue
		}
		key, rest, ok := splitKey(line)
		if !ok {
			continue // ignored nested content or blank line
		}
		flush()
		if !isTrackedKey(key) {
			pendingKey = ignoredKey
			blockFolded = false
			block = nil
			if rest == "" {
				// Consuming an ignored nested mapping or block keeps
				// indented lines out of tracked fields.
				pendingKey = ignoredKey
			}
			continue
		}
		if rest == "" {
			pendingKey = key
			block = nil
			blockFolded = true // plain multi-line scalars fold like >
			continue
		}
		if isBlockIndicator(rest) {
			pendingKey = key
			blockFolded = strings.HasPrefix(strings.TrimSpace(rest), ">")
			block = nil
			continue
		}
		assignField(&fm, key, unquote(strings.TrimSpace(rest)))
	}
	flush()

	body := strings.Join(lines[end+1:], "\n")
	body = strings.TrimPrefix(body, "\n")
	return fm, body, nil
}

// ignoredKey marks a pending block owned by a field the tool ignores.
const ignoredKey = "\x00ignored"

func isTrackedKey(key string) bool {
	return key == "name" || key == "description" || key == "compatibility"
}

// splitKey extracts a top-level "key:" pair. Indented lines and lines
// without a colon at the top level report not-ok.
func splitKey(line string) (key, rest string, ok bool) {
	if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '#' {
		return "", "", false
	}
	trimmed := strings.TrimRight(line, "\r")
	idx := strings.Index(trimmed, ":")
	if idx <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(trimmed[:idx])
	if strings.ContainsAny(key, " \t\"'{}[],&*") {
		return "", "", false
	}
	return key, strings.TrimSpace(trimmed[idx+1:]), true
}

// isBlockLine reports whether line continues a pending block scalar:
// any indented line or blank line.
func isBlockLine(line string) bool {
	return line == "" || line[0] == ' ' || line[0] == '\t'
}

// isBlockIndicator reports whether value opens a block scalar (|,
// |-, >, >-, possibly with chomping and indent indicators).
func isBlockIndicator(value string) bool {
	v := strings.TrimSpace(value)
	if v == "" || (v[0] != '|' && v[0] != '>') {
		return false
	}
	rest := strings.TrimRight(v[1:], "+-")
	for i := 0; i < len(rest); i++ {
		if rest[i] < '0' || rest[i] > '9' {
			return false
		}
	}
	return true
}

// renderBlock joins collected block lines: literal blocks join with
// newlines; folded blocks join non-blank runs with spaces, blank lines
// becoming newlines. The lines' common indentation is stripped and
// trailing whitespace clipped.
func renderBlock(lines []string, folded bool) string {
	if len(lines) == 0 {
		return ""
	}
	indent := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		n := len(line) - len(strings.TrimLeft(line, " \t"))
		if indent == -1 || n < indent {
			indent = n
		}
	}
	if indent < 0 {
		indent = 0
	}
	trimmed := make([]string, len(lines))
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		trimmed[i] = line[indent:]
	}
	lines = trimmed
	if !folded {
		return strings.TrimRight(strings.Join(lines, "\n"), "\n")
	}
	var b strings.Builder
	for i, line := range lines {
		if line == "" {
			b.WriteString("\n")
			continue
		}
		if i > 0 && lines[i-1] != "" {
			b.WriteString(" ")
		}
		b.WriteString(line)
	}
	return strings.TrimRight(b.String(), "\n ")
}

// unquote strips surrounding quotes from a scalar value and resolves
// the two escapes a double-quoted scalar may carry.
func unquote(value string) string {
	if len(value) >= 2 {
		if value[0] == '"' && value[len(value)-1] == '"' {
			inner := value[1 : len(value)-1]
			inner = strings.ReplaceAll(inner, `\"`, `"`)
			return strings.ReplaceAll(inner, `\\`, `\`)
		}
		if value[0] == '\'' && value[len(value)-1] == '\'' {
			return strings.ReplaceAll(value[1:len(value)-1], "''", "'")
		}
	}
	return value
}

func assignField(fm *frontmatter, key, value string) {
	switch key {
	case "name":
		fm.name = value
	case "description":
		fm.description = value
	case "compatibility":
		fm.compatibility = value
	}
}

func splitLines(content string) []string {
	return strings.Split(content, "\n")
}
