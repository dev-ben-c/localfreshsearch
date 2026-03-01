package tools

import (
	"strings"
	"unicode"
)

// sanitize removes control characters and trims whitespace from a string.
// Preserves newlines and tabs for formatting.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return -1 // drop
		}
		return r
	}, s)
}

// truncate limits a string to maxLen characters, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// wrapToolOutput frames tool output so the LLM can distinguish data from instructions.
func wrapToolOutput(toolName, content string) string {
	return "[TOOL RESULT: " + toolName + "]\n" + content + "\n[END TOOL RESULT]"
}
