package harness

import "strings"

func composeSystemPrompt(parts ...string) string {
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			segments = append(segments, part)
		}
	}
	return strings.Join(segments, "\n\n")
}
