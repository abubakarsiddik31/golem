package pdfextract

import (
	"regexp"
	"strings"
)

var (
	// Mojibake mapping for common double-encoded UTF-8 sequences (Windows-1252 / ISO-8859-1 interpreted as UTF-8)
	mojibakeReplacer = strings.NewReplacer(
		"â€œ", "“",
		"â€\u009d", "”",
		"â€ ", "”",
		"â€\u009c", "“",
		"â€˜", "‘",
		"â€™", "’",
		"â€“", "–",
		"â€”", "—",
		"â€¢", "•",
		"âˆ†", "∆",
		"â‰¥", "≥",
		"â‰¤", "≤",
		"â†’", "→",
		"â† ", "←",
		"â‡’", "⇒",
		"Â±", "±",
		"Ã©", "é",
		"Ã ", "à",
		"Ã¨", "è",
		"Ã±", "ñ",
		"Ã¼", "ü",
		"Ã¶", "ö",
		"Ã¤", "ä",
		"âˆž", "∞",
		"â‰ˆ", "≈",
		"â‰ ", "≠",
		"âˆ‚", "∂",
		"âˆ‘", "∑",
		"âˆ«", "∫",
		"Â°", "°",
	)

	// Unicode typographical ligatures to standard ASCII letter pairs
	ligatureReplacer = strings.NewReplacer(
		"\uFB00", "ff",
		"\uFB01", "fi",
		"\uFB02", "fl",
		"\uFB03", "ffi",
		"\uFB04", "ffl",
		"\uFB05", "ft",
		"\uFB06", "st",
		"ﬁ", "fi",
		"ﬂ", "fl",
		"ﬀ", "ff",
		"ﬃ", "ffi",
		"ﬄ", "ffl",
	)

	hyphenBreakRegex = regexp.MustCompile(`(\b[a-zA-Z]{2,})-\s*\n\s*([a-zA-Z]{2,}\b)`)
	sqrtRegex        = regexp.MustCompile(`√\s*([a-zA-Z0-9_^{}]+)`)
)

// CleanText normalizes character encoding, resolves typographical ligatures, and cleans symbols.
func CleanText(s string) string {
	s = mojibakeReplacer.Replace(s)
	s = ligatureReplacer.Replace(s)
	s = normalizeMathSymbols(s)
	// General square root normalization
	s = sqrtRegex.ReplaceAllString(s, "\\sqrt{$1}")
	return s
}

// normalizeMathSymbols maps Mathematical Alphanumeric Unicode symbols to standard ASCII and Greek characters.
func normalizeMathSymbols(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch {
		case r >= 0x1D400 && r <= 0x1D419: // Bold A-Z
			sb.WriteRune('A' + (r - 0x1D400))
		case r >= 0x1D41A && r <= 0x1D433: // Bold a-z
			sb.WriteRune('a' + (r - 0x1D41A))
		case r >= 0x1D434 && r <= 0x1D44D: // Italic A-Z
			sb.WriteRune('A' + (r - 0x1D434))
		case r >= 0x1D44E && r <= 0x1D467: // Italic a-z
			sb.WriteRune('a' + (r - 0x1D44E))
		case r >= 0x1D468 && r <= 0x1D481: // Bold Italic A-Z
			sb.WriteRune('A' + (r - 0x1D468))
		case r >= 0x1D482 && r <= 0x1D49B: // Bold Italic a-z
			sb.WriteRune('a' + (r - 0x1D482))
		case r >= 0x1D5A0 && r <= 0x1D5B9: // Sans-serif A-Z
			sb.WriteRune('A' + (r - 0x1D5A0))
		case r >= 0x1D5BA && r <= 0x1D5D3: // Sans-serif a-z
			sb.WriteRune('a' + (r - 0x1D5BA))
		case r >= 0x1D7CE && r <= 0x1D7D7: // Bold digits 0-9
			sb.WriteRune('0' + (r - 0x1D7CE))
		case r >= 0x1D6E2 && r <= 0x1D6FB: // Math Greek Capital A..Omega
			sb.WriteRune(rune(0x0391 + (r - 0x1D6E2)))
		case r >= 0x1D6FC && r <= 0x1D715: // Math Greek Small alpha..omega
			sb.WriteRune(rune(0x03B1 + (r - 0x1D6FC)))
		case r == 0x1D716: // epsilon symbol
			sb.WriteRune('ε')
		case r == 0x1D717: // vartheta
			sb.WriteRune('θ')
		case r == 0x1D718: // varkappa
			sb.WriteRune('κ')
		case r == 0x1D719: // phi symbol
			sb.WriteRune('ϕ')
		case r == 0x1D71A: // varrho
			sb.WriteRune('ρ')
		case r == 0x1D71B: // varpi
			sb.WriteRune('ϖ')
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// RepairHyphenation rejoins words that were split by end-of-line hyphens across line breaks.
func RepairHyphenation(s string) string {
	return hyphenBreakRegex.ReplaceAllString(s, "$1$2")
}
