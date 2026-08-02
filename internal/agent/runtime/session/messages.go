package session

import (
	"time"

	protocol "oops/internal/agent/ai"
)

const (
	compactionSummaryPrefix = "The conversation history before this point was compacted into the following summary:\n\n<summary>\n"
	compactionSummarySuffix = "\n</summary>"
	branchSummaryPrefix     = "The following is a summary of a branch that this conversation came back from:\n\n<summary>\n"
	branchSummarySuffix     = "</summary>"
)

func CompactionSummaryMessage(summary string, timestamp time.Time) protocol.UserMessage {
	return protocol.UserMessage{
		Content: protocol.ContentList{
			protocol.NewTextContent(compactionSummaryPrefix + summary + compactionSummarySuffix),
		},
		Timestamp: timestamp.UnixMilli(),
	}
}

func BranchSummaryMessage(summary string, timestamp time.Time) protocol.UserMessage {
	return protocol.UserMessage{
		Content: protocol.ContentList{
			protocol.NewTextContent(branchSummaryPrefix + summary + branchSummarySuffix),
		},
		Timestamp: timestamp.UnixMilli(),
	}
}
