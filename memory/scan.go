package memory

import (
	"regexp"
	"strings"
	"unicode"
)

// Finding is one suspicious pattern in memory content. Threat names the
// category; Pattern names the matching rule. Neither includes the matched
// text, so findings can be shown to a model or logged without quoting an
// attack.
type Finding struct {
	Threat  string
	Pattern string
}

// ArtifactScanner checks text before it is stored or loaded. Scan returns
// nil or an empty slice when the text is acceptable.
type ArtifactScanner interface {
	Scan(text string) []Finding
}

// Scanner is the built-in, dependency-free artifact scanner. It checks
// for common prompt-injection phrasing, chat-template role overrides,
// credential-exfiltration commands, hidden HTML comments, and invisible
// Unicode controls. It is pattern matching, not a trust boundary.
type Scanner struct{}

// NewScanner returns the built-in scanner.
func NewScanner() *Scanner { return &Scanner{} }

type scanRule struct {
	threat  string
	pattern string
	re      *regexp.Regexp
}

var scanRules = []scanRule{
	{
		threat: "injection", pattern: "ignore_instructions",
		re: regexp.MustCompile(`(?i)\b(ignore|disregard|forget)\b[^.\n]{0,40}\b(previous|prior|above|system)\b[^.\n]{0,20}\b(instructions?|prompts?|rules?)\b`),
	},
	{
		threat: "role_override", pattern: "role_prefix",
		re: regexp.MustCompile(`(?i)(^|\n)\s*(system|assistant|developer)\s*:`),
	},
	{
		threat: "role_override", pattern: "chat_template",
		re: regexp.MustCompile(`(?i)<\|im_start\|>|\[INST\]`),
	},
	{
		threat: "exfiltration", pattern: "credential_command",
		re: regexp.MustCompile(`(?i)\b(curl|wget)\b[^\n]{0,120}(\$[A-Z0-9_]*(KEY|TOKEN|SECRET)|\.ssh|\.env|authorized_keys)`),
	},
	{
		threat: "exfiltration", pattern: "credential_file",
		re: regexp.MustCompile(`(?i)\bcat\b[^\n]{0,80}(\.ssh/|\.env\b|authorized_keys)`),
	},
	{
		threat: "html_comment", pattern: "html_comment",
		re: regexp.MustCompile(`<!--`),
	},
}

// Scan returns unique findings in rule order, followed by invisible
// Unicode findings.
func (s *Scanner) Scan(text string) []Finding {
	var findings []Finding
	seen := make(map[Finding]struct{})
	add := func(finding Finding) {
		if _, ok := seen[finding]; ok {
			return
		}
		seen[finding] = struct{}{}
		findings = append(findings, finding)
	}
	for _, rule := range scanRules {
		if rule.re.MatchString(text) {
			add(Finding{Threat: rule.threat, Pattern: rule.pattern})
		}
	}
	for i, r := range text {
		if invisibleRune(r, i == 0) {
			add(Finding{Threat: "invisible_unicode", Pattern: "invisible_control"})
			break
		}
	}
	return findings
}

func invisibleRune(r rune, first bool) bool {
	if r == '\uFEFF' {
		return !first
	}
	switch {
	case r == '\u200B', r == '\u2060', r == '\u180E', r == '\u3164', r == '\uFFA0':
		return true
	case r >= '\u2061' && r <= '\u2064':
		return true
	case r >= '\u202A' && r <= '\u202E':
		return true
	case r >= '\u2066' && r <= '\u2069':
		return true
	case unicode.Is(unicode.Cf, r) && r != '\u200C' && r != '\u200D' && r != '\u200E' && r != '\u200F':
		return true
	}
	return false
}

// findingSummary names the threats without quoting content.
func findingSummary(findings []Finding) string {
	seen := make(map[string]struct{})
	var threats []string
	for _, finding := range findings {
		if _, ok := seen[finding.Threat]; ok {
			continue
		}
		seen[finding.Threat] = struct{}{}
		threats = append(threats, finding.Threat)
	}
	return strings.Join(threats, ", ")
}
