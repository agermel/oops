package runtime

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"

	protocol "oops/internal/agent/ai"
	aiutils "oops/internal/agent/ai/utils"
)

type SummaryDetails struct {
	ReadFiles     []string `json:"readFiles"`
	ModifiedFiles []string `json:"modifiedFiles"`
}

func (s *Session) SummaryDetailsBefore(firstKeptEntryID string) (SummaryDetails, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	path := s.pathToLeafLocked(s.leafID)
	index := indexEntry(path, firstKeptEntryID)
	if index < 0 {
		return SummaryDetails{}, errors.New("first kept entry is not on current path")
	}
	return collectSummaryDetails(path[:index]), nil
}

func (s *Session) BranchSummaryDetails(targetID string) (SummaryDetails, error) {
	entries, _, err := s.EntriesForBranchSummary(targetID)
	if err != nil {
		return SummaryDetails{}, err
	}
	return collectSummaryDetails(entries), nil
}

func (s *Session) EntriesForBranchSummary(targetID string) ([]Entry, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if targetID != "" {
		if _, ok := s.entries[targetID]; !ok {
			return nil, "", errors.New("target entry is not in session")
		}
	}
	oldPath := s.pathToLeafLocked(s.leafID)
	if len(oldPath) == 0 {
		return nil, "", nil
	}
	targetPath := s.pathToLeafLocked(targetID)
	targetIDs := map[string]bool{}
	for _, entry := range targetPath {
		targetIDs[entry.ID] = true
	}
	commonAncestorID := ""
	for i := len(oldPath) - 1; i >= 0; i-- {
		if targetIDs[oldPath[i].ID] {
			commonAncestorID = oldPath[i].ID
			break
		}
	}
	start := 0
	if commonAncestorID != "" {
		for i, entry := range oldPath {
			if entry.ID == commonAncestorID {
				start = i + 1
				break
			}
		}
	}
	return cloneEntries(oldPath[start:]), commonAncestorID, nil
}

func (s *Session) ProtectedFirstKeptEntryID(firstKeptEntryID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	path := s.pathToLeafLocked(s.leafID)
	index := indexEntry(path, firstKeptEntryID)
	if index < 0 {
		return "", errors.New("first kept entry is not on current path")
	}
	if callID, ok := toolResultCallID(path[index]); ok {
		for i := index - 1; i >= 0; i-- {
			if assistantHasToolCall(path[i], callID) {
				return path[i].ID, nil
			}
		}
	}
	return firstKeptEntryID, nil
}

func (s *Session) FirstKeptEntryID(keepRecentTokens int) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	path := s.pathToLeafLocked(s.leafID)
	if len(path) == 0 {
		return "", nil
	}
	if keepRecentTokens <= 0 {
		return s.protectedFirstKeptEntryIDLocked(path[len(path)-1].ID, path)
	}
	total := 0
	first := path[len(path)-1].ID
	for i := len(path) - 1; i >= 0; i-- {
		tokens := EstimateEntryTokens(path[i])
		if tokens == 0 {
			continue
		}
		first = path[i].ID
		total += tokens
		if total >= keepRecentTokens {
			break
		}
	}
	return s.protectedFirstKeptEntryIDLocked(first, path)
}

func (s *Session) protectedFirstKeptEntryIDLocked(firstKeptEntryID string, path []Entry) (string, error) {
	index := indexEntry(path, firstKeptEntryID)
	if index < 0 {
		return "", errors.New("first kept entry is not on current path")
	}
	if callID, ok := toolResultCallID(path[index]); ok {
		for i := index - 1; i >= 0; i-- {
			if assistantHasToolCall(path[i], callID) {
				return path[i].ID, nil
			}
		}
	}
	return firstKeptEntryID, nil
}

func EstimateEntryTokens(entry Entry) int {
	switch entry.Type {
	case EntryMessage, EntryCustomMessage:
		return protocol.EstimateMessageTokens(entry.Message)
	case EntryCompaction, EntryBranchSummary:
		return aiutils.EstimateTextTokens(entry.Summary)
	default:
		return 0
	}
}

func collectSummaryDetails(entries []Entry) SummaryDetails {
	ops := fileOps{read: map[string]bool{}, modified: map[string]bool{}}
	for _, entry := range entries {
		mergeEntryDetails(entry, &ops)
		extractEntryFileOps(entry, &ops)
	}
	return ops.details()
}

type fileOps struct {
	read     map[string]bool
	modified map[string]bool
}

func (o fileOps) details() SummaryDetails {
	read := make([]string, 0, len(o.read))
	for path := range o.read {
		if !o.modified[path] {
			read = append(read, path)
		}
	}
	modified := make([]string, 0, len(o.modified))
	for path := range o.modified {
		modified = append(modified, path)
	}
	sort.Strings(read)
	sort.Strings(modified)
	return SummaryDetails{ReadFiles: read, ModifiedFiles: modified}
}

func mergeEntryDetails(entry Entry, ops *fileOps) {
	if len(entry.Details) == 0 {
		return
	}
	var details SummaryDetails
	if err := json.Unmarshal(entry.Details, &details); err != nil {
		return
	}
	for _, path := range details.ReadFiles {
		addPath(ops.read, path)
	}
	for _, path := range details.ModifiedFiles {
		addPath(ops.modified, path)
	}
}

func extractEntryFileOps(entry Entry, ops *fileOps) {
	if assistant, ok := protocol.AsAssistantMessage(entry.Message); ok {
		extractAssistantFileOps(assistant, ops)
		return
	}
	if result, ok := protocol.AsToolResultMessage(entry.Message); ok {
		extractToolResultFileOps(result, ops)
	}
}

func extractAssistantFileOps(message protocol.AssistantMessage, ops *fileOps) {
	for _, item := range message.Content {
		call, ok := asToolCall(item)
		if !ok {
			continue
		}
		path := pathFromRawArguments(call.Arguments)
		if path == "" {
			continue
		}
		switch call.Name {
		case "read":
			addPath(ops.read, path)
		case "write", "edit":
			addPath(ops.modified, path)
		}
	}
}

func extractToolResultFileOps(message protocol.ToolResultMessage, ops *fileOps) {
	path := pathFromDetails(message.Details)
	if path == "" {
		return
	}
	switch message.ToolName {
	case "read":
		addPath(ops.read, path)
	case "write", "edit":
		addPath(ops.modified, path)
	}
}

func asToolCall(item protocol.Content) (protocol.ToolCallContent, bool) {
	switch value := item.(type) {
	case protocol.ToolCallContent:
		return value, true
	case *protocol.ToolCallContent:
		if value != nil {
			return *value, true
		}
	}
	return protocol.ToolCallContent{}, false
}

func pathFromRawArguments(raw json.RawMessage) string {
	var args struct {
		Path     string `json:"path"`
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return ""
	}
	if args.Path != "" {
		return args.Path
	}
	return args.FilePath
}

func pathFromDetails(details any) string {
	if details == nil {
		return ""
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return ""
	}
	var fields struct {
		Path     string `json:"path"`
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return ""
	}
	if fields.Path != "" {
		return fields.Path
	}
	return fields.FilePath
}

func addPath(target map[string]bool, path string) {
	path = strings.TrimSpace(path)
	if path != "" {
		target[path] = true
	}
}

func toolResultCallID(entry Entry) (string, bool) {
	if message, ok := protocol.AsToolResultMessage(entry.Message); ok && message.ToolCallID != "" {
		return message.ToolCallID, true
	}
	return "", false
}

func assistantHasToolCall(entry Entry, callID string) bool {
	message, ok := protocol.AsAssistantMessage(entry.Message)
	if !ok {
		return false
	}
	for _, item := range message.Content {
		call, ok := asToolCall(item)
		if ok && call.ID == callID {
			return true
		}
	}
	return false
}

func indexEntry(entries []Entry, id string) int {
	for i, entry := range entries {
		if entry.ID == id {
			return i
		}
	}
	return -1
}

func marshalDetails(details any) (json.RawMessage, error) {
	if details == nil {
		return nil, nil
	}
	if raw, ok := details.(json.RawMessage); ok {
		return cloneRaw(raw), nil
	}
	data, err := json.Marshal(details)
	if err != nil {
		return nil, err
	}
	return data, nil
}
