package session

import (
	"testing"
	"time"

	protocol "oops/internal/agent/ai"
)

func TestSummaryMessagesUseStableContextBoundaries(t *testing.T) {
	timestamp := time.UnixMilli(1234)
	tests := []struct {
		name    string
		message protocol.UserMessage
		want    string
	}{
		{
			name:    "compaction",
			message: CompactionSummaryMessage("older facts", timestamp),
			want:    "The conversation history before this point was compacted into the following summary:\n\n<summary>\nolder facts\n</summary>",
		},
		{
			name:    "branch",
			message: BranchSummaryMessage("previous branch", timestamp),
			want:    "The following is a summary of a branch that this conversation came back from:\n\n<summary>\nprevious branch</summary>",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.message.Timestamp != timestamp.UnixMilli() {
				t.Fatalf("timestamp = %d, want %d", test.message.Timestamp, timestamp.UnixMilli())
			}
			if got := protocol.TextFromContent(test.message.Content); got != test.want {
				t.Fatalf("text = %q, want %q", got, test.want)
			}
		})
	}
}
