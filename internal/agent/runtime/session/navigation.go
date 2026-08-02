package session

import (
	"fmt"

	protocol "oops/internal/agent/ai"
)

type NavigationResult struct {
	LeafID     string
	EditorText string
}

func (s *Session) ResolveNavigationTarget(targetID string) (NavigationResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.resolveNavigationTargetLocked(targetID)
}

func (s *Session) ValidateContext() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ctx := s.buildContextLocked()
	return validateMessageSequence(ctx.Messages)
}

func (s *Session) ValidateMessages(messages protocol.MessageList) error {
	return validateMessageSequence(messages)
}

func (s *Session) resolveNavigationTargetLocked(targetID string) (NavigationResult, error) {
	if targetID == "" {
		return NavigationResult{}, nil
	}
	entry, ok := s.entries[targetID]
	if !ok {
		return NavigationResult{}, fmt.Errorf("unknown leaf %q", targetID)
	}
	if text, ok := editableMessageText(entry); ok {
		return NavigationResult{LeafID: entry.ParentID, EditorText: text}, nil
	}
	if assistantToolCallIDs(entry) != nil {
		if leafID, ok := s.completeToolUnitLeafLocked(entry.ID, ""); ok {
			return NavigationResult{LeafID: leafID}, nil
		}
		return NavigationResult{LeafID: s.safeBoundaryLocked(entry.ParentID)}, nil
	}
	if _, ok := toolResultCallID(entry); ok {
		assistant, ok := s.nearestAssistantToolCallLocked(entry)
		if !ok {
			return NavigationResult{LeafID: s.safeBoundaryLocked(entry.ParentID)}, nil
		}
		if leafID, ok := s.completeToolUnitLeafLocked(assistant.ID, entry.ID); ok {
			return NavigationResult{LeafID: leafID}, nil
		}
		return NavigationResult{LeafID: s.safeBoundaryLocked(assistant.ParentID)}, nil
	}
	return NavigationResult{LeafID: targetID}, nil
}

func editableMessageText(entry Entry) (string, bool) {
	switch entry.Type {
	case EntryMessage:
		return userMessageText(entry.Message)
	case EntryCustomMessage:
		return userMessageText(entry.Message)
	default:
		return "", false
	}
}

func userMessageText(message protocol.AgentMessage) (string, bool) {
	user, ok := protocol.AsUserMessage(message)
	if !ok {
		return "", false
	}
	return protocol.TextFromContent(user.Content), true
}

func (s *Session) nearestAssistantToolCallLocked(entry Entry) (Entry, bool) {
	currentID := entry.ParentID
	seen := map[string]bool{}
	for currentID != "" {
		if seen[currentID] {
			return Entry{}, false
		}
		seen[currentID] = true
		current, ok := s.entries[currentID]
		if !ok {
			return Entry{}, false
		}
		if assistantToolCallIDs(current) != nil {
			return current, true
		}
		if _, ok := toolResultCallID(current); !ok {
			return Entry{}, false
		}
		currentID = current.ParentID
	}
	return Entry{}, false
}

func (s *Session) completeToolUnitLeafLocked(assistantID, requiredEntryID string) (string, bool) {
	assistant, ok := s.entries[assistantID]
	if !ok {
		return "", false
	}
	expected := assistantToolCallIDs(assistant)
	if len(expected) == 0 {
		return "", false
	}
	candidates := s.toolUnitCandidatesLocked(assistantID, expected)
	if requiredEntryID != "" {
		for _, candidate := range candidates {
			if candidate.contains[requiredEntryID] {
				return candidate.leafID, true
			}
		}
		return "", false
	}
	activePath := s.activePathIDsLocked()
	for _, candidate := range candidates {
		if activePath[candidate.leafID] {
			return candidate.leafID, true
		}
	}
	if len(candidates) == 1 {
		return candidates[0].leafID, true
	}
	return "", false
}

type toolUnitCandidate struct {
	leafID   string
	contains map[string]bool
}

func (s *Session) toolUnitCandidatesLocked(parentID string, expected map[string]bool) []toolUnitCandidate {
	var candidates []toolUnitCandidate
	var walk func(parent string, seen map[string]bool, contains map[string]bool)
	walk = func(parent string, seen map[string]bool, contains map[string]bool) {
		if len(seen) == len(expected) {
			candidateContains := map[string]bool{}
			for id := range contains {
				candidateContains[id] = true
			}
			candidates = append(candidates, toolUnitCandidate{leafID: parent, contains: candidateContains})
			return
		}
		for _, child := range s.childrenLocked(parent) {
			callID, ok := toolResultCallID(child)
			if !ok || !expected[callID] || seen[callID] {
				continue
			}
			nextSeen := map[string]bool{}
			for id := range seen {
				nextSeen[id] = true
			}
			nextSeen[callID] = true
			nextContains := map[string]bool{}
			for id := range contains {
				nextContains[id] = true
			}
			nextContains[child.ID] = true
			walk(child.ID, nextSeen, nextContains)
		}
	}
	walk(parentID, map[string]bool{}, map[string]bool{parentID: true})
	return candidates
}

func (s *Session) childrenLocked(parentID string) []Entry {
	children := make([]Entry, 0)
	for _, id := range s.order {
		entry := s.entries[id]
		if entry.ParentID == parentID {
			children = append(children, entry)
		}
	}
	return children
}

func (s *Session) activePathIDsLocked() map[string]bool {
	out := map[string]bool{}
	for _, entry := range s.pathToLeafLocked(s.leafID) {
		out[entry.ID] = true
	}
	return out
}

func (s *Session) safeBoundaryLocked(leafID string) string {
	for {
		ctx := s.buildContextForLeafLocked(leafID)
		if validateMessageSequence(ctx.Messages) == nil {
			return leafID
		}
		if leafID == "" {
			return ""
		}
		entry, ok := s.entries[leafID]
		if !ok {
			return ""
		}
		leafID = entry.ParentID
	}
}

func assistantToolCallIDs(entry Entry) map[string]bool {
	assistant, ok := protocol.AsAssistantMessage(entry.Message)
	if !ok {
		return nil
	}
	out := map[string]bool{}
	for _, item := range assistant.Content {
		call, ok := asToolCall(item)
		if ok {
			out[call.ID] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
