package compaction

import (
	"context"
	"time"

	protocol "oops/internal/agent/ai"
	"oops/internal/agent/runtime/session"
)

const defaultBranchContextWindow = 128000

type BranchPreparation struct {
	Messages       protocol.MessageList
	FileOperations FileOperations
	TotalTokens    int
}

type BranchResult struct {
	Summary       string
	ReadFiles     []string
	ModifiedFiles []string
}

type BranchOptions struct {
	Completer           Completer
	Model               protocol.Model
	CustomInstructions  string
	ReplaceInstructions bool
	ReserveTokens       int
}

// PrepareBranchEntries selects the newest branch messages within the token budget.
func PrepareBranchEntries(entries []session.Entry, tokenBudget int) BranchPreparation {
	operations := NewFileOperations()
	for _, entry := range entries {
		if entry.Type == session.EntryBranchSummary {
			mergeEntryDetails(&operations, entry)
		}
	}

	messages := make(protocol.MessageList, 0, len(entries))
	totalTokens := 0
	for index := len(entries) - 1; index >= 0; index-- {
		entry := entries[index]
		ExtractFileOperations(entry.Message, &operations)
		message := messageFromEntry(entry, true)
		if message == nil {
			continue
		}

		tokens := session.EstimateEntryTokens(entry)
		if tokenBudget > 0 && totalTokens+tokens > tokenBudget {
			if (entry.Type == session.EntryCompaction || entry.Type == session.EntryBranchSummary) && totalTokens*10 < tokenBudget*9 {
				messages = append(protocol.MessageList{message}, messages...)
				totalTokens += tokens
			}
			break
		}
		messages = append(protocol.MessageList{message}, messages...)
		totalTokens += tokens
	}
	return BranchPreparation{
		Messages:       messages,
		FileOperations: operations,
		TotalTokens:    totalTokens,
	}
}

// GenerateBranchSummary summarizes an abandoned Session branch.
func GenerateBranchSummary(ctx context.Context, entries []session.Entry, options BranchOptions) (BranchResult, error) {
	reserveTokens := options.ReserveTokens
	if reserveTokens == 0 {
		reserveTokens = defaultReserveTokens
	}
	contextWindow := options.Model.ContextWindow
	if contextWindow == 0 {
		contextWindow = defaultBranchContextWindow
	}
	preparation := PrepareBranchEntries(entries, contextWindow-reserveTokens)
	if len(preparation.Messages) == 0 {
		return BranchResult{Summary: "No content to summarize"}, nil
	}

	instructions := branchSummaryPrompt
	if options.ReplaceInstructions && options.CustomInstructions != "" {
		instructions = options.CustomInstructions
	} else if options.CustomInstructions != "" {
		instructions += "\n\nAdditional focus: " + options.CustomInstructions
	}
	promptText := "<conversation>\n" + SerializeConversation(preparation.Messages) + "\n</conversation>\n\n" + instructions
	text, err := completeText(ctx, options.Completer, CompleteRequest{
		Model: options.Model,
		Context: protocol.Context{
			SystemPrompt: summarizationSystemPrompt,
			Messages: protocol.MessageList{protocol.UserMessage{
				Content:   protocol.ContentList{protocol.NewTextContent(promptText)},
				Timestamp: time.Now().UnixMilli(),
			}},
		},
		MaxTokens: 2048,
	}, "Branch summary")
	if err != nil {
		return BranchResult{}, err
	}

	readFiles, modifiedFiles := ComputeFileLists(preparation.FileOperations)
	summary := branchSummaryPreamble + text + FormatFileOperations(readFiles, modifiedFiles)
	if summary == "" {
		summary = "No summary generated"
	}
	return BranchResult{
		Summary:       summary,
		ReadFiles:     readFiles,
		ModifiedFiles: modifiedFiles,
	}, nil
}

const branchSummaryPreamble = `The user explored a different conversation branch before returning here.
Summary of that exploration:

`

const branchSummaryPrompt = `Create a structured summary of this conversation branch for context when returning later.

Use this EXACT format:

## Goal
[What was the user trying to accomplish in this branch?]

## Constraints & Preferences
- [Any constraints, preferences, or requirements mentioned]
- [Or "(none)" if none were mentioned]

## Progress
### Done
- [x] [Completed tasks/changes]

### In Progress
- [ ] [Work that was started but not finished]

### Blocked
- [Issues preventing progress, if any]

## Key Decisions
- **[Decision]**: [Brief rationale]

## Next Steps
1. [What should happen next to continue this work]

Keep each section concise. Preserve exact file paths, function names, and error messages.`
