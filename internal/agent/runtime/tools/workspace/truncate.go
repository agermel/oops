package workspace

import (
	"strings"
	"unicode/utf8"
)

type truncationDetails struct {
	Truncated             bool   `json:"truncated"`
	TruncatedBy           string `json:"truncatedBy,omitempty"`
	TotalLines            int    `json:"totalLines"`
	TotalBytes            int    `json:"totalBytes"`
	OutputLines           int    `json:"outputLines"`
	OutputBytes           int    `json:"outputBytes"`
	LastLinePartial       bool   `json:"lastLinePartial,omitempty"`
	FirstLineExceedsLimit bool   `json:"firstLineExceedsLimit,omitempty"`
	MaxLines              int    `json:"maxLines"`
	MaxBytes              int    `json:"maxBytes"`
}

type truncationResult struct {
	content string
	details truncationDetails
}

func (c workspaceToolConfig) truncateHead(content string) truncationResult {
	return truncateHead(content, c.maxLines, c.maxBytes)
}

func (c workspaceToolConfig) truncateTail(content string) truncationResult {
	return truncateTail(content, c.maxLines, c.maxBytes)
}

func truncateHead(content string, maxLines, maxBytes int) truncationResult {
	lines := strings.Split(content, "\n")
	totalLines := len(lines)
	totalBytes := len([]byte(content))
	details := truncationDetails{
		TotalLines:  totalLines,
		TotalBytes:  totalBytes,
		MaxLines:    maxLines,
		MaxBytes:    maxBytes,
		OutputLines: totalLines,
		OutputBytes: totalBytes,
	}
	if totalLines <= maxLines && totalBytes <= maxBytes {
		return truncationResult{content: content, details: details}
	}
	details.Truncated = true
	if len(lines) > 0 && len([]byte(lines[0])) > maxBytes {
		details.TruncatedBy = "bytes"
		details.FirstLineExceedsLimit = true
		details.OutputLines = 0
		details.OutputBytes = 0
		return truncationResult{details: details}
	}
	out := make([]string, 0, minInt(totalLines, maxLines))
	usedBytes := 0
	truncatedBy := "lines"
	for i := 0; i < totalLines && i < maxLines; i++ {
		lineBytes := len([]byte(lines[i]))
		if i > 0 {
			lineBytes++
		}
		if usedBytes+lineBytes > maxBytes {
			truncatedBy = "bytes"
			break
		}
		out = append(out, lines[i])
		usedBytes += lineBytes
	}
	output := strings.Join(out, "\n")
	details.TruncatedBy = truncatedBy
	details.OutputLines = len(out)
	details.OutputBytes = len([]byte(output))
	return truncationResult{content: output, details: details}
}

func truncateTail(content string, maxLines, maxBytes int) truncationResult {
	lines := strings.Split(content, "\n")
	if len(lines) > 1 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	totalLines := len(lines)
	totalBytes := len([]byte(content))
	details := truncationDetails{
		TotalLines:  totalLines,
		TotalBytes:  totalBytes,
		MaxLines:    maxLines,
		MaxBytes:    maxBytes,
		OutputLines: totalLines,
		OutputBytes: totalBytes,
	}
	if totalLines <= maxLines && totalBytes <= maxBytes {
		return truncationResult{content: content, details: details}
	}
	details.Truncated = true
	out := make([]string, 0, minInt(totalLines, maxLines))
	usedBytes := 0
	truncatedBy := "lines"
	for i := totalLines - 1; i >= 0 && len(out) < maxLines; i-- {
		lineBytes := len([]byte(lines[i]))
		if len(out) > 0 {
			lineBytes++
		}
		if usedBytes+lineBytes > maxBytes {
			truncatedBy = "bytes"
			if len(out) == 0 {
				line := truncateStringFromEnd(lines[i], maxBytes)
				out = append([]string{line}, out...)
				details.LastLinePartial = true
			}
			break
		}
		out = append([]string{lines[i]}, out...)
		usedBytes += lineBytes
	}
	output := strings.Join(out, "\n")
	details.TruncatedBy = truncatedBy
	details.OutputLines = len(out)
	details.OutputBytes = len([]byte(output))
	return truncationResult{content: output, details: details}
}

func splitLinesForCounting(content string) []string {
	lines := strings.Split(content, "\n")
	if len(lines) > 1 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func truncateStringFromEnd(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len([]byte(value)) <= maxBytes {
		return value
	}
	start := len(value)
	used := 0
	for start > 0 {
		r, size := utf8.DecodeLastRuneInString(value[:start])
		if r == utf8.RuneError && size == 0 {
			break
		}
		runeBytes := len([]byte(string(r)))
		if used+runeBytes > maxBytes {
			break
		}
		used += runeBytes
		start -= size
	}
	return value[start:]
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
