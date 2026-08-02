package workspace

import (
	"strings"
	"testing"
)

func TestTruncateCountsEmptyAndTrailingLines(t *testing.T) {
	tests := []struct {
		name       string
		result     truncationResult
		totalLines int
	}{
		{name: "head empty", result: truncateHead("", 10, 10), totalLines: 1},
		{name: "tail empty", result: truncateTail("", 10, 10), totalLines: 1},
		{name: "head trailing newline", result: truncateHead("a\n", 10, 10), totalLines: 2},
		{name: "tail trailing newline", result: truncateTail("a\n", 10, 10), totalLines: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.result.details.TotalLines != test.totalLines {
				t.Fatalf("TotalLines = %d, want %d", test.result.details.TotalLines, test.totalLines)
			}
		})
	}
}

func TestTruncateHeadUTF8ByteLimit(t *testing.T) {
	result := truncateHead("éé\nabc", 10, 4)
	if result.content != "éé" {
		t.Fatalf("content = %q, want %q", result.content, "éé")
	}
	if !result.details.Truncated || result.details.TruncatedBy != "bytes" || result.details.OutputBytes != 4 {
		t.Fatalf("details = %#v", result.details)
	}
	if result.details.FirstLineExceedsLimit {
		t.Fatalf("FirstLineExceedsLimit = true")
	}

	result = truncateHead("éé\nabc", 10, 3)
	if result.content != "" || !result.details.FirstLineExceedsLimit {
		t.Fatalf("result = %#v", result)
	}
}

func TestTruncateTailUTF8ByteBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		maxBytes int
		want     string
	}{
		{name: "partial multibyte line", content: "aé🙂b", maxBytes: 5, want: "🙂b"},
		{name: "oversized final rune", content: "abc🙂", maxBytes: 3, want: ""},
		{name: "zero limit", content: "abc", maxBytes: 0, want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := truncateTail(test.content, 10, test.maxBytes)
			if result.content != test.want {
				t.Fatalf("content = %q, want %q", result.content, test.want)
			}
			if !result.details.Truncated || result.details.TruncatedBy != "bytes" || !result.details.LastLinePartial {
				t.Fatalf("details = %#v", result.details)
			}
			if result.details.OutputBytes > test.maxBytes {
				t.Fatalf("OutputBytes = %d, max = %d", result.details.OutputBytes, test.maxBytes)
			}
		})
	}
}

func TestTruncateTailOversizedLineWithTrailingNewline(t *testing.T) {
	result := truncateTail(strings.Repeat("X", 300_000)+"\n", 100, 1024)
	if result.content != strings.Repeat("X", 1024) {
		t.Fatalf("content length = %d, want 1024", len(result.content))
	}
	if result.details.OutputLines != 1 || result.details.OutputBytes != 1024 || !result.details.LastLinePartial {
		t.Fatalf("details = %#v", result.details)
	}
}

func TestTruncateTailNeverSplitsUTF8(t *testing.T) {
	inputs := []string{"aé🙂b", "中文🙂abc", "👩‍💻"}
	for _, input := range inputs {
		for maxBytes := 0; maxBytes <= len([]byte(input))+2; maxBytes++ {
			result := truncateTail(input, 10, maxBytes)
			if !strings.HasSuffix(input, result.content) {
				t.Fatalf("input = %q, maxBytes = %d, content = %q", input, maxBytes, result.content)
			}
			if len([]byte(result.content)) > maxBytes {
				t.Fatalf("input = %q, maxBytes = %d, OutputBytes = %d", input, maxBytes, len([]byte(result.content)))
			}
		}
	}
}
