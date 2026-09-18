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
	// General square root normalization
	s = sqrtRegex.ReplaceAllString(s, "\\sqrt{$1}")
	return s
}

// RepairHyphenation rejoins words that were split by end-of-line hyphens across line breaks.
func RepairHyphenation(s string) string {
	return hyphenBreakRegex.ReplaceAllString(s, "$1$2")
}
