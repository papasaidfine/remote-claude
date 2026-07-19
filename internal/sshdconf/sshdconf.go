// Package sshdconf provides pure text transforms for editing an sshd_config in
// place: setting a global directive and commenting lines out. Kept separate so
// it is unit-testable without touching a real sshd_config.
package sshdconf

import (
	"regexp"
	"strings"
)

// SetDirective replaces the first line that sets name (whether active or
// commented) with "name value"; if none exists, it appends the directive.
func SetDirective(text, name, value string) string {
	re := regexp.MustCompile(`(?m)^[#\t ]*` + regexp.QuoteMeta(name) + `([ \t][^\r\n]*)?\r?$`)
	line := name + " " + value
	if loc := re.FindStringIndex(text); loc != nil {
		return text[:loc[0]] + line + text[loc[1]:]
	}
	trimmed := strings.TrimRight(text, "\r\n")
	if trimmed == "" {
		return line + "\n"
	}
	return trimmed + "\n" + line + "\n"
}

// CommentOut prefixes every line matching pattern with "# <prefix>: ",
// preserving the original line. Already-commented lines no longer match, so
// this is idempotent for the caller's re-runs.
func CommentOut(text, pattern, prefix string) string {
	re := regexp.MustCompile(pattern)
	return re.ReplaceAllStringFunc(text, func(m string) string {
		return "# " + prefix + ": " + m
	})
}
