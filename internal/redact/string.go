package redact

import (
	"regexp"
	"unicode/utf8"
)

var (
	privateKey = regexp.MustCompile(`(?s)-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY-----.*?(?:-----END (?:[A-Z0-9]+ )*PRIVATE KEY-----|\z)`)
	assignment = regexp.MustCompile(`(?i)\b(?:password|passwd|secret|token|api[_-]?key|access[_-]?key|auth[_-]?key|secret[_-]?key)["']?\s*[:=]\s*(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;"'<>` + "`" + `]+)`)
	valueStart = regexp.MustCompile(`[:=]\s*`)
	bearer     = regexp.MustCompile(`(?i)\bBearer[ \t]+[^\s,"'<>;]+`)
	credential = regexp.MustCompile(`\b(?:AKIA[A-Z0-9]{12,}|AKLT[A-Za-z0-9_-]{12,}|sk-[A-Za-z0-9_-]{8,}|ghp_[A-Za-z0-9]{8,}|glpat-[A-Za-z0-9_-]{8,}|xox[bp]-[A-Za-z0-9-]{8,}|eyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)`)
)

// String masks known credential forms, retaining at most four leading runes.
// Short secrets are fully masked; PEM private keys are replaced as whole blocks.
func String(s string) string {
	s = privateKey.ReplaceAllString(s, "[REDACTED PRIVATE KEY]")
	// Standalone forms first: assignment handling remains idempotent on masks.
	s = credential.ReplaceAllStringFunc(s, mask)
	s = bearer.ReplaceAllStringFunc(s, func(s string) string {
		i := len("Bearer")
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		return s[:i] + mask(s[i:])
	})
	return assignment.ReplaceAllStringFunc(s, func(s string) string {
		loc := valueStart.FindStringIndex(s)
		prefix, value := s[:loc[1]], s[loc[1]:]
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') {
			return prefix + value[:1] + mask(value[1:len(value)-1]) + value[len(value)-1:]
		}
		return prefix + mask(value)
	})
}

func mask(s string) string {
	if s == "" {
		return s
	}
	if utf8.RuneCountInString(s) <= 4 {
		return "****"
	}
	runes := []rune(s)
	return string(runes[:4]) + "****"
}
