package harness

import (
	"errors"
)

func validateCompaction(summary, firstKeptEntryID string) error {
	if summary == "" {
		return errors.New("compaction summary is required")
	}
	if firstKeptEntryID == "" {
		return errors.New("compaction first kept entry id is required")
	}
	return nil
}
