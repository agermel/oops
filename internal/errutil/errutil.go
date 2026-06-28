// Package errutil provides shared error detection and sanitization utilities
// used across the project — cleaning MCP/Pydantic traces, detecting tool errors,
// and formatting HTTP error responses.
package errutil

import (
	"regexp"
	"strings"
)

// pydanticTraceRe matches Pydantic validation traceback URLs to strip them
// from error messages before showing to users.
var pydanticTraceRe = regexp.MustCompile(`\n For further information visit https?://[^\s]+`)

// Sanitize cleans tool/MCP-layer error messages by removing Pydantic
// tracebacks and MCP protocol details, keeping only the user-relevant parts.
func Sanitize(raw string) string {
	s := pydanticTraceRe.ReplaceAllString(raw, "")
	s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	s = strings.TrimSpace(s)
	if s == "" {
		return raw
	}
	return s
}

// ErrorIndicators is a list of substrings that suggest a tool result
// contains an error. MCP servers may return isError:true (protocol-standard)
// or embed error text in content (e.g. "Error retrieving Redis info: ...").
var ErrorIndicators = []string{
	`"isError"`,
	"Error ", "error ", "ERROR ",
	"failed to ", "Failed to ",
	"invalid ", "Invalid ",
	"refused", "Refused",
	"timeout", "Timeout",
	"unauthorized", "Unauthorized",
	"permission denied", "Permission denied",
	"not found", "Not found",
}

// ContainsError checks whether a tool result string contains known error
// indicators.
func ContainsError(content string) bool {
	for _, indicator := range ErrorIndicators {
		if strings.Contains(content, indicator) {
			return true
		}
	}
	return false
}
