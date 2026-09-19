package docextract

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

func extractMarkdown(r io.Reader, opts Options) (*Document, error) {
	scanner := bufio.NewScanner(r)
	var (
		lines        []string
		outlineItems []OutlineItem
		metadata     = make(map[string]string)
		firstTitle   string

		inFrontmatter bool
		fmBuf         strings.Builder
		lineNum       int
	)

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// Detect YAML frontmatter at the very beginning
		if lineNum == 1 && strings.TrimSpace(line) == "---" {
			inFrontmatter = true
			continue
		}
		if inFrontmatter {
			if strings.TrimSpace(line) == "---" {
				inFrontmatter = false
				parseFrontmatter(fmBuf.String(), metadata)
				continue
			}
			fmBuf.WriteString(line)
			fmBuf.WriteString("\n")
			continue
		}

		lines = append(lines, line)

		// Check for ATX heading: # Heading
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			hashes := 0
			for hashes < len(trimmed) && trimmed[hashes] == '#' {
				hashes++
			}
			if hashes <= 6 && hashes < len(trimmed) && (trimmed[hashes] == ' ' || trimmed[hashes] == '\t') {
				headingTitle := strings.TrimSpace(trimmed[hashes:])
				outlineItems = append(outlineItems, OutlineItem{
					Level:    hashes,
					Title:    headingTitle,
					Location: fmt.Sprintf("Line %d", lineNum),
				})
				if firstTitle == "" {
					firstTitle = headingTitle
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("docextract: read markdown: %w", err)
	}

	fullContent := strings.Join(lines, "\n")
	content := fullContent

	if opts.Query != "" {
		filtered := extractSectionByQuery(fullContent, opts.Query)
		if filtered != "" {
			content = filtered
		}
	}

	doc := &Document{
		Format:   FormatMarkdown,
		Title:    firstTitle,
		Content:  content,
		Outline:  outlineItems,
		Metadata: metadata,
	}
	return doc, nil
}

func parseFrontmatter(raw string, meta map[string]string) {
	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			k := strings.TrimSpace(parts[0])
			v := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
			meta[k] = v
		}
	}
}

// extractSectionByQuery searches for a heading matching query, and returns
// that heading and all content under it until the next heading of equal or higher rank.
func extractSectionByQuery(content string, query string) string {
	qLower := strings.ToLower(strings.TrimSpace(query))
	lines := strings.Split(content, "\n")

	matchedStart := -1
	matchedLevel := 0

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			hashes := 0
			for hashes < len(trimmed) && trimmed[hashes] == '#' {
				hashes++
			}
			if hashes <= 6 && hashes < len(trimmed) && (trimmed[hashes] == ' ' || trimmed[hashes] == '\t') {
				headingText := strings.TrimSpace(trimmed[hashes:])
				if strings.Contains(strings.ToLower(headingText), qLower) {
					matchedStart = i
					matchedLevel = hashes
					break
				}
			}
		}
	}

	if matchedStart == -1 {
		// Fallback: look for section matching "## Sheet: <query>" or "## Slide <num>: <query>"
		for i, line := range lines {
			if strings.Contains(strings.ToLower(line), qLower) && strings.HasPrefix(strings.TrimSpace(line), "##") {
				matchedStart = i
				matchedLevel = 2
				break
			}
		}
	}

	if matchedStart == -1 {
		return ""
	}

	// Collect until next heading of <= matchedLevel
	var extracted []string
	extracted = append(extracted, lines[matchedStart])

	for i := matchedStart + 1; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			hashes := 0
			for hashes < len(trimmed) && trimmed[hashes] == '#' {
				hashes++
			}
			if hashes <= 6 && hashes < len(trimmed) && (trimmed[hashes] == ' ' || trimmed[hashes] == '\t') {
				if hashes <= matchedLevel {
					break
				}
			}
		}
		extracted = append(extracted, line)
	}

	return strings.TrimSpace(strings.Join(extracted, "\n"))
}

func extractPlainText(r io.Reader, opts Options) (*Document, error) {
	scanner := bufio.NewScanner(r)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("docextract: read text: %w", err)
	}
	content := strings.Join(lines, "\n")
	return &Document{
		Format:  FormatText,
		Content: content,
	}, nil
}
