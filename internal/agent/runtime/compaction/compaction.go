package compaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	protocol "oops/internal/agent/ai"
	"oops/internal/agent/runtime/session"
)

const (
	defaultReserveTokens    = 16384
	defaultKeepRecentTokens = 20000
)

var DefaultSettings = Settings{
	Enabled:          true,
	ReserveTokens:    defaultReserveTokens,
	KeepRecentTokens: defaultKeepRecentTokens,
}

type ErrorCode string

const (
	CodeAborted             ErrorCode = "aborted"
	CodeSummarizationFailed ErrorCode = "summarization_failed"
	CodeInvalidSession      ErrorCode = "invalid_session"
	CodeUnknown             ErrorCode = "unknown"
)

// OperationError preserves a stable error category for Harness callers.
type OperationError struct {
	Code    ErrorCode
	Message string
	Err     error
}

func (e *OperationError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return string(e.Code)
}

func (e *OperationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type Settings struct {
	Enabled          bool
	ReserveTokens    int
	KeepRecentTokens int
}

type CompleteRequest struct {
	Model     protocol.Model
	Context   protocol.Context
	Reasoning string
	MaxTokens int
}

type Completer func(context.Context, CompleteRequest) (protocol.AssistantMessage, error)

type ContextUsageEstimate struct {
	Tokens         int
	UsageTokens    int
	TrailingTokens int
	LastUsageIndex int
	HasUsage       bool
}

type CutPoint struct {
	FirstKeptEntryIndex int
	TurnStartIndex      int
	SplitTurn           bool
}

type Preparation struct {
	FirstKeptEntryID string
	Messages         protocol.MessageList
	TurnPrefix       protocol.MessageList
	SplitTurn        bool
	TokensBefore     int
	PreviousSummary  string
	FileOperations   FileOperations
	Settings         Settings
}

type Details = session.SummaryDetails

type Result struct {
	Summary          string
	FirstKeptEntryID string
	TokensBefore     int
	Details          Details
}

type SummaryOptions struct {
	Completer          Completer
	Model              protocol.Model
	ReserveTokens      int
	CustomInstructions string
	PreviousSummary    string
	Reasoning          string
}

type CompactOptions struct {
	Completer          Completer
	Model              protocol.Model
	CustomInstructions string
	Reasoning          string
}

func CalculateContextTokens(usage protocol.Usage) int {
	if usage.TotalTokens > 0 {
		return usage.TotalTokens
	}
	return usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
}

// EstimateContextTokens combines the most recent provider usage with trailing estimates.
func EstimateContextTokens(messages protocol.MessageList) ContextUsageEstimate {
	for index := len(messages) - 1; index >= 0; index-- {
		assistant, ok := protocol.AsAssistantMessage(messages[index])
		if !ok || assistant.StopReason == protocol.StopReasonAborted || assistant.StopReason == protocol.StopReasonError {
			continue
		}
		usageTokens := CalculateContextTokens(assistant.Usage)
		if usageTokens <= 0 {
			continue
		}
		trailingTokens := 0
		for _, message := range messages[index+1:] {
			trailingTokens += protocol.EstimateMessageTokens(message)
		}
		return ContextUsageEstimate{
			Tokens:         usageTokens + trailingTokens,
			UsageTokens:    usageTokens,
			TrailingTokens: trailingTokens,
			LastUsageIndex: index,
			HasUsage:       true,
		}
	}

	estimated := 0
	for _, message := range messages {
		estimated += protocol.EstimateMessageTokens(message)
	}
	return ContextUsageEstimate{
		Tokens:         estimated,
		TrailingTokens: estimated,
		LastUsageIndex: -1,
	}
}

func ShouldCompact(contextTokens, contextWindow int, settings Settings) bool {
	return settings.Enabled && contextTokens > contextWindow-settings.ReserveTokens
}

// FindTurnStartIndex finds the user-visible start of the turn containing entryIndex.
func FindTurnStartIndex(entries []session.Entry, entryIndex, startIndex int) int {
	for index := entryIndex; index >= startIndex; index-- {
		entry := entries[index]
		if entry.Type == session.EntryBranchSummary || entry.Type == session.EntryCustomMessage {
			return index
		}
		if entry.Type == session.EntryMessage && entry.Message != nil && entry.Message.MessageRole() == protocol.RoleUser {
			return index
		}
	}
	return -1
}

// FindCutPoint selects the first retained entry for the requested recent-token budget.
func FindCutPoint(entries []session.Entry, startIndex, endIndex, keepRecentTokens int) CutPoint {
	if startIndex < 0 {
		startIndex = 0
	}
	if endIndex > len(entries) {
		endIndex = len(entries)
	}
	if startIndex >= endIndex {
		return CutPoint{FirstKeptEntryIndex: startIndex, TurnStartIndex: -1}
	}

	cutPoints := validCutPoints(entries, startIndex, endIndex)
	if len(cutPoints) == 0 {
		return CutPoint{FirstKeptEntryIndex: startIndex, TurnStartIndex: -1}
	}

	accumulatedTokens := 0
	cutIndex := cutPoints[0]
	for index := endIndex - 1; index >= startIndex; index-- {
		entry := entries[index]
		if entry.Type != session.EntryMessage && entry.Type != session.EntryCustomMessage {
			continue
		}
		accumulatedTokens += session.EstimateEntryTokens(entry)
		if accumulatedTokens < keepRecentTokens {
			continue
		}
		for _, candidate := range cutPoints {
			if candidate >= index {
				cutIndex = candidate
				break
			}
		}
		break
	}

	for cutIndex > startIndex {
		previous := entries[cutIndex-1]
		if previous.Type == session.EntryCompaction || previous.Type == session.EntryMessage || previous.Type == session.EntryCustomMessage {
			break
		}
		cutIndex--
	}
	cutIndex = protectToolUnit(entries, cutIndex, startIndex)

	cutEntry := entries[cutIndex]
	isUserMessage := cutEntry.Type == session.EntryMessage && cutEntry.Message != nil && cutEntry.Message.MessageRole() == protocol.RoleUser
	turnStartIndex := -1
	if !isUserMessage {
		turnStartIndex = FindTurnStartIndex(entries, cutIndex, startIndex)
	}
	return CutPoint{
		FirstKeptEntryIndex: cutIndex,
		TurnStartIndex:      turnStartIndex,
		SplitTurn:           !isUserMessage && turnStartIndex >= 0,
	}
}

// Prepare selects the active Session path content needed for one compaction run.
func Prepare(current *session.Session, settings Settings) (*Preparation, error) {
	path, err := activePath(current)
	if err != nil {
		return nil, err
	}
	if len(path) == 0 || path[len(path)-1].Type == session.EntryCompaction {
		return nil, nil
	}

	previousCompactionIndex := -1
	for index := len(path) - 1; index >= 0; index-- {
		if path[index].Type == session.EntryCompaction {
			previousCompactionIndex = index
			break
		}
	}

	previousSummary := ""
	boundaryStart := 0
	if previousCompactionIndex >= 0 {
		previous := path[previousCompactionIndex]
		previousSummary = previous.Summary
		if firstKeptIndex := entryIndex(path, previous.FirstKeptEntryID); firstKeptIndex >= 0 {
			boundaryStart = firstKeptIndex
		} else {
			boundaryStart = previousCompactionIndex + 1
		}
	}

	cutPoint := FindCutPoint(path, boundaryStart, len(path), settings.KeepRecentTokens)
	if cutPoint.FirstKeptEntryIndex < 0 || cutPoint.FirstKeptEntryIndex >= len(path) {
		return nil, operationError(CodeInvalidSession, "compaction retained entry is outside the active path", nil)
	}
	firstKeptEntry := path[cutPoint.FirstKeptEntryIndex]
	if firstKeptEntry.ID == "" {
		return nil, operationError(CodeInvalidSession, "compaction retained entry requires an id", nil)
	}

	historyEnd := cutPoint.FirstKeptEntryIndex
	if cutPoint.SplitTurn {
		historyEnd = cutPoint.TurnStartIndex
	}
	messages := messagesFromEntries(path, boundaryStart, historyEnd, true)
	turnPrefix := protocol.MessageList(nil)
	if cutPoint.SplitTurn {
		turnPrefix = messagesFromEntries(path, cutPoint.TurnStartIndex, cutPoint.FirstKeptEntryIndex, true)
	}

	fileOperations := collectPreparationFileOperations(path, boundaryStart, cutPoint.FirstKeptEntryIndex, previousCompactionIndex)
	tokensBefore := EstimateContextTokens(current.BuildContext().Messages).Tokens
	return &Preparation{
		FirstKeptEntryID: firstKeptEntry.ID,
		Messages:         messages,
		TurnPrefix:       turnPrefix,
		SplitTurn:        cutPoint.SplitTurn,
		TokensBefore:     tokensBefore,
		PreviousSummary:  previousSummary,
		FileOperations:   fileOperations,
		Settings:         settings,
	}, nil
}

// GenerateSummary creates or updates the structured summary for compacted history.
func GenerateSummary(ctx context.Context, messages protocol.MessageList, options SummaryOptions) (string, error) {
	basePrompt := summaryPrompt
	if options.PreviousSummary != "" {
		basePrompt = updateSummaryPrompt
	}
	if options.CustomInstructions != "" {
		basePrompt += "\n\nAdditional focus: " + options.CustomInstructions
	}

	promptText := "<conversation>\n" + SerializeConversation(messages) + "\n</conversation>\n\n"
	if options.PreviousSummary != "" {
		promptText += "<previous-summary>\n" + options.PreviousSummary + "\n</previous-summary>\n\n"
	}
	promptText += basePrompt

	return completeText(ctx, options.Completer, CompleteRequest{
		Model: options.Model,
		Context: protocol.Context{
			SystemPrompt: summarizationSystemPrompt,
			Messages: protocol.MessageList{protocol.UserMessage{
				Content:   protocol.ContentList{protocol.NewTextContent(promptText)},
				Timestamp: time.Now().UnixMilli(),
			}},
		},
		Reasoning: enabledReasoning(options.Reasoning),
		MaxTokens: outputLimit(options.ReserveTokens*4/5, options.Model.MaxTokens),
	}, "Summarization")
}

// Compact turns a prepared history range into data ready for a Session compaction entry.
func Compact(ctx context.Context, preparation Preparation, options CompactOptions) (Result, error) {
	if preparation.FirstKeptEntryID == "" {
		return Result{}, operationError(CodeInvalidSession, "compaction retained entry requires an id", nil)
	}

	var summary string
	if preparation.SplitTurn && len(preparation.TurnPrefix) > 0 {
		type outcome struct {
			text string
			err  error
		}
		historyResult := make(chan outcome, 1)
		prefixResult := make(chan outcome, 1)
		go func() {
			if len(preparation.Messages) == 0 {
				historyResult <- outcome{text: "No prior history."}
				return
			}
			text, err := GenerateSummary(ctx, preparation.Messages, SummaryOptions{
				Completer:          options.Completer,
				Model:              options.Model,
				ReserveTokens:      preparation.Settings.ReserveTokens,
				CustomInstructions: options.CustomInstructions,
				PreviousSummary:    preparation.PreviousSummary,
				Reasoning:          options.Reasoning,
			})
			historyResult <- outcome{text: text, err: err}
		}()
		go func() {
			text, err := generateTurnPrefixSummary(ctx, preparation.TurnPrefix, options, preparation.Settings.ReserveTokens)
			prefixResult <- outcome{text: text, err: err}
		}()

		history := <-historyResult
		prefix := <-prefixResult
		if history.err != nil {
			return Result{}, history.err
		}
		if prefix.err != nil {
			return Result{}, prefix.err
		}
		summary = history.text + "\n\n---\n\n**Turn Context (split turn):**\n\n" + prefix.text
	} else {
		generated, err := GenerateSummary(ctx, preparation.Messages, SummaryOptions{
			Completer:          options.Completer,
			Model:              options.Model,
			ReserveTokens:      preparation.Settings.ReserveTokens,
			CustomInstructions: options.CustomInstructions,
			PreviousSummary:    preparation.PreviousSummary,
			Reasoning:          options.Reasoning,
		})
		if err != nil {
			return Result{}, err
		}
		summary = generated
	}

	readFiles, modifiedFiles := ComputeFileLists(preparation.FileOperations)
	details := Details{ReadFiles: readFiles, ModifiedFiles: modifiedFiles}
	return Result{
		Summary:          summary + FormatFileOperations(readFiles, modifiedFiles),
		FirstKeptEntryID: preparation.FirstKeptEntryID,
		TokensBefore:     preparation.TokensBefore,
		Details:          details,
	}, nil
}

func generateTurnPrefixSummary(ctx context.Context, messages protocol.MessageList, options CompactOptions, reserveTokens int) (string, error) {
	promptText := "<conversation>\n" + SerializeConversation(messages) + "\n</conversation>\n\n" + turnPrefixSummaryPrompt
	return completeText(ctx, options.Completer, CompleteRequest{
		Model: options.Model,
		Context: protocol.Context{
			SystemPrompt: summarizationSystemPrompt,
			Messages: protocol.MessageList{protocol.UserMessage{
				Content:   protocol.ContentList{protocol.NewTextContent(promptText)},
				Timestamp: time.Now().UnixMilli(),
			}},
		},
		Reasoning: enabledReasoning(options.Reasoning),
		MaxTokens: outputLimit(reserveTokens/2, options.Model.MaxTokens),
	}, "Turn prefix summarization")
}

func completeText(ctx context.Context, completer Completer, request CompleteRequest, operation string) (string, error) {
	if completer == nil {
		return "", operationError(CodeSummarizationFailed, strings.ToLower(operation)+" completer is required", nil)
	}
	if err := ctx.Err(); err != nil {
		return "", operationError(CodeAborted, err.Error(), err)
	}
	response, err := completer(ctx, request)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "", operationError(CodeAborted, err.Error(), err)
		}
		return "", operationError(CodeSummarizationFailed, operation+" failed: "+err.Error(), err)
	}
	switch response.StopReason {
	case protocol.StopReasonAborted:
		message := response.ErrorMessage
		if message == "" {
			message = operation + " aborted"
		}
		return "", operationError(CodeAborted, message, nil)
	case protocol.StopReasonError:
		message := response.ErrorMessage
		if message == "" {
			message = "Unknown error"
		}
		return "", operationError(CodeSummarizationFailed, operation+" failed: "+message, nil)
	default:
		return assistantText(response), nil
	}
}

func activePath(current *session.Session) ([]session.Entry, error) {
	if current == nil {
		return nil, operationError(CodeInvalidSession, "compaction requires a session", nil)
	}
	leafID := current.LeafID()
	if leafID == "" {
		return nil, nil
	}
	entries := current.Entries()
	byID := make(map[string]session.Entry, len(entries))
	for _, entry := range entries {
		byID[entry.ID] = entry
	}

	path := make([]session.Entry, 0)
	seen := make(map[string]struct{})
	for entryID := leafID; entryID != ""; {
		if _, repeated := seen[entryID]; repeated {
			return nil, operationError(CodeInvalidSession, "session active path contains a cycle", nil)
		}
		seen[entryID] = struct{}{}
		entry, ok := byID[entryID]
		if !ok {
			return nil, operationError(CodeInvalidSession, fmt.Sprintf("session active path references unknown entry %q", entryID), nil)
		}
		path = append(path, entry)
		entryID = entry.ParentID
	}
	slices.Reverse(path)
	return path, nil
}

func validCutPoints(entries []session.Entry, startIndex, endIndex int) []int {
	cutPoints := make([]int, 0)
	for index := startIndex; index < endIndex; index++ {
		entry := entries[index]
		switch entry.Type {
		case session.EntryBranchSummary, session.EntryCustomMessage:
			cutPoints = append(cutPoints, index)
		case session.EntryMessage:
			if entry.Message == nil || entry.Message.MessageRole() == protocol.RoleToolResult {
				continue
			}
			cutPoints = append(cutPoints, index)
		}
	}
	return cutPoints
}

func protectToolUnit(entries []session.Entry, cutIndex, startIndex int) int {
	if cutIndex < 0 || cutIndex >= len(entries) {
		return cutIndex
	}
	result, ok := protocol.AsToolResultMessage(entries[cutIndex].Message)
	if !ok {
		return cutIndex
	}
	for index := cutIndex - 1; index >= startIndex; index-- {
		assistant, ok := protocol.AsAssistantMessage(entries[index].Message)
		if !ok {
			continue
		}
		for _, call := range protocol.ToolCallsFromAssistant(assistant) {
			if call.ID == result.ToolCallID {
				return index
			}
		}
	}
	return cutIndex
}

func messagesFromEntries(entries []session.Entry, startIndex, endIndex int, skipCompaction bool) protocol.MessageList {
	messages := make(protocol.MessageList, 0, endIndex-startIndex)
	for index := startIndex; index < endIndex; index++ {
		entry := entries[index]
		if skipCompaction && entry.Type == session.EntryCompaction {
			continue
		}
		if message := messageFromEntry(entry, false); message != nil {
			messages = append(messages, message)
		}
	}
	return messages
}

func messageFromEntry(entry session.Entry, skipToolResult bool) protocol.AgentMessage {
	switch entry.Type {
	case session.EntryMessage, session.EntryCustomMessage:
		if skipToolResult && entry.Message != nil && entry.Message.MessageRole() == protocol.RoleToolResult {
			return nil
		}
		return protocol.CloneMessage(entry.Message)
	case session.EntryBranchSummary:
		return session.BranchSummaryMessage(entry.Summary, entry.Timestamp)
	case session.EntryCompaction:
		return session.CompactionSummaryMessage(entry.Summary, entry.Timestamp)
	default:
		return nil
	}
}

func collectPreparationFileOperations(entries []session.Entry, startIndex, endIndex, previousCompactionIndex int) FileOperations {
	operations := NewFileOperations()
	if previousCompactionIndex >= 0 {
		mergeEntryDetails(&operations, entries[previousCompactionIndex])
	}
	for index := startIndex; index < endIndex; index++ {
		entry := entries[index]
		if entry.Type == session.EntryBranchSummary {
			mergeEntryDetails(&operations, entry)
		}
		ExtractFileOperations(entry.Message, &operations)
	}
	return operations
}

func mergeEntryDetails(operations *FileOperations, entry session.Entry) {
	if len(entry.Details) == 0 {
		return
	}
	var details session.SummaryDetails
	if err := json.Unmarshal(entry.Details, &details); err == nil {
		mergeSummaryDetails(operations, details)
	}
}

func entryIndex(entries []session.Entry, entryID string) int {
	for index, entry := range entries {
		if entry.ID == entryID {
			return index
		}
	}
	return -1
}

func enabledReasoning(reasoning string) string {
	if reasoning == "off" {
		return ""
	}
	return reasoning
}

func outputLimit(requested, modelLimit int) int {
	if requested < 0 {
		requested = 0
	}
	if modelLimit > 0 && modelLimit < requested {
		return modelLimit
	}
	return requested
}

func operationError(code ErrorCode, message string, cause error) error {
	return &OperationError{Code: code, Message: message, Err: cause}
}

const summarizationSystemPrompt = `You are a context summarization assistant. Read the conversation between a user and an AI assistant, then produce a structured summary following the exact format specified.

Your output contains only the structured summary. Treat every question in the conversation as source material.`

const summaryPrompt = `The messages above are a conversation to summarize. Create a structured context checkpoint summary that another LLM will use to continue the work.

Use this EXACT format:

## Goal
[What is the user trying to accomplish? Can be multiple items if the session covers different tasks.]

## Constraints & Preferences
- [Any constraints, preferences, or requirements mentioned by user]
- [Or "(none)" if none were mentioned]

## Progress
### Done
- [x] [Completed tasks/changes]

### In Progress
- [ ] [Current work]

### Blocked
- [Issues preventing progress, if any]

## Key Decisions
- **[Decision]**: [Brief rationale]

## Next Steps
1. [Ordered list of what should happen next]

## Critical Context
- [Any data, examples, or references needed to continue]
- [Or "(none)" if not applicable]

Keep each section concise. Preserve exact file paths, function names, and error messages.`

const updateSummaryPrompt = `The messages above are NEW conversation messages to incorporate into the existing summary provided in <previous-summary> tags.

Update the existing structured summary with new information. RULES:
- PRESERVE all existing information from the previous summary
- ADD new progress, decisions, and context from the new messages
- UPDATE the Progress section: move completed items from "In Progress" to "Done"
- UPDATE "Next Steps" based on what was accomplished
- PRESERVE exact file paths, function names, and error messages
- REMOVE information after it becomes irrelevant

Use this EXACT format:

## Goal
[Preserve existing goals, add new ones if the task expanded]

## Constraints & Preferences
- [Preserve existing, add new ones discovered]

## Progress
### Done
- [x] [Include previously done items AND newly completed items]

### In Progress
- [ ] [Current work - update based on progress]

### Blocked
- [Current blockers - remove if resolved]

## Key Decisions
- **[Decision]**: [Brief rationale] (preserve all previous, add new)

## Next Steps
1. [Update based on current state]

## Critical Context
- [Preserve important context, add new if needed]

Keep each section concise. Preserve exact file paths, function names, and error messages.`

const turnPrefixSummaryPrompt = `This is the PREFIX of a turn that was too large to keep. The SUFFIX (recent work) is retained.

Summarize the prefix to provide context for the retained suffix:

## Original Request
[What did the user ask for in this turn?]

## Early Progress
- [Key decisions and work done in the prefix]

## Context for Suffix
- [Information needed to understand the retained recent work]

Be concise. Focus on the context needed to understand the kept suffix.`
