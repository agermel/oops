package session

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"oops/internal/llm/ai/protocol"
)

type Session struct {
	mu sync.RWMutex

	id        string
	cwd       string
	projectID string
	name      string
	title     string
	entries   map[string]Entry
	order     []string
	leafID    string
	createdAt time.Time
	updatedAt time.Time
}

func New(id string) *Session {
	if id == "" {
		id = newSessionID()
	}
	now := time.Now()
	return &Session{
		id:        id,
		entries:   make(map[string]Entry),
		createdAt: now,
		updatedAt: now,
	}
}

func (s *Session) ID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.id
}

func (s *Session) LeafID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.leafID
}

func (s *Session) Info() Info {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Info{
		ID:        s.id,
		CWD:       s.cwd,
		ProjectID: s.projectID,
		Name:      s.name,
		Title:     s.title,
		Summary:   s.summaryLocked(),
		LeafID:    s.leafID,
		CreatedAt: s.createdAt,
		UpdatedAt: s.updatedAt,
		Entries:   len(s.entries),
		Messages:  s.messageCountLocked(),
	}
}

func (s *Session) Entries() []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries := make([]Entry, 0, len(s.order))
	for _, id := range s.order {
		entries = append(entries, cloneEntry(s.entries[id]))
	}
	return entries
}

func (s *Session) summaryLocked() string {
	for _, id := range s.order {
		entry := s.entries[id]
		if entry.Type != EntryMessage || entry.Message == nil {
			continue
		}
		if entry.Message.MessageRole() != protocol.RoleUser {
			continue
		}
		if text := strings.Join(contentText(entry.Message), " "); text != "" {
			return strings.Join(strings.Fields(text), " ")
		}
	}
	return ""
}

func contentText(message protocol.AgentMessage) []string {
	var content protocol.ContentList
	switch typed := message.(type) {
	case protocol.UserMessage:
		content = typed.Content
	case *protocol.UserMessage:
		content = typed.Content
	case protocol.AssistantMessage:
		content = typed.Content
	case *protocol.AssistantMessage:
		content = typed.Content
	case protocol.ToolResultMessage:
		content = typed.Content
	case *protocol.ToolResultMessage:
		content = typed.Content
	}
	out := make([]string, 0, len(content))
	for _, item := range content {
		switch typed := item.(type) {
		case protocol.TextContent:
			if typed.Text != "" {
				out = append(out, typed.Text)
			}
		case *protocol.TextContent:
			if typed != nil && typed.Text != "" {
				out = append(out, typed.Text)
			}
		}
	}
	return out
}

func (s *Session) Entry(id string) (Entry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.entries[id]
	return cloneEntry(entry), ok
}

func (s *Session) AppendSessionInfo(cwd, name string) (Entry, error) {
	return s.AppendSessionInfoWithProject(cwd, name, "")
}

func (s *Session) AppendSessionInfoWithProject(cwd, name, projectID string) (Entry, error) {
	return s.append(Entry{Type: EntrySessionInfo, CWD: cwd, Name: name, ProjectID: projectID})
}

func (s *Session) AppendMessage(message protocol.AgentMessage) (Entry, error) {
	return s.append(Entry{Type: EntryMessage, Message: protocol.CloneMessage(message)})
}

func (s *Session) AppendModelChange(provider, model string) (Entry, error) {
	return s.append(Entry{Type: EntryModelChange, Provider: provider, Model: model})
}

func (s *Session) AppendThinkingLevelChange(reasoning string) (Entry, error) {
	return s.append(Entry{Type: EntryThinkingLevelChange, Reasoning: reasoning})
}

func (s *Session) AppendActiveToolsChange(toolNames []string) (Entry, error) {
	return s.append(Entry{Type: EntryActiveToolsChange, ToolNames: cloneStrings(toolNames)})
}

func (s *Session) AppendCompaction(summary, firstKeptEntryID string, tokensBefore int) (Entry, error) {
	return s.AppendCompactionWithDetails(summary, firstKeptEntryID, tokensBefore, nil)
}

func (s *Session) AppendCompactionWithDetails(summary, firstKeptEntryID string, tokensBefore int, details any) (Entry, error) {
	rawDetails, err := marshalDetails(details)
	if err != nil {
		return Entry{}, err
	}
	return s.append(Entry{
		Type:             EntryCompaction,
		Summary:          summary,
		FirstKeptEntryID: firstKeptEntryID,
		TokensBefore:     tokensBefore,
		Details:          rawDetails,
	})
}

func (s *Session) AppendBranchSummary(summary string) (Entry, error) {
	return s.AppendBranchSummaryWithDetails(summary, nil)
}

func (s *Session) AppendBranchSummaryWithDetails(summary string, details any) (Entry, error) {
	rawDetails, err := marshalDetails(details)
	if err != nil {
		return Entry{}, err
	}
	return s.append(Entry{Type: EntryBranchSummary, Summary: summary, Details: rawDetails})
}

func (s *Session) AppendCustomEntry(customType string, payload []byte) (Entry, error) {
	return s.append(Entry{Type: EntryCustom, CustomType: customType, Payload: cloneRaw(payload)})
}

func (s *Session) AppendCustomMessageEntry(message protocol.AgentMessage) (Entry, error) {
	return s.append(Entry{Type: EntryCustomMessage, Message: protocol.CloneMessage(message)})
}

func (s *Session) AppendLabel(label string) (Entry, error) {
	return s.append(Entry{Type: EntryLabel, Label: label})
}

func (s *Session) AppendSessionName(name string) (Entry, error) {
	return s.append(Entry{Type: EntrySessionInfo, Name: name})
}

func (s *Session) AppendSessionTitle(title string) (Entry, error) {
	return s.append(Entry{Type: EntrySessionInfo, Title: title})
}

func (s *Session) AppendLeaf(leafID string) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if leafID != "" {
		if _, ok := s.entries[leafID]; !ok {
			return Entry{}, fmt.Errorf("unknown leaf %q", leafID)
		}
	}
	entry := Entry{Type: EntryLeaf, LeafID: leafID}
	if err := s.prepareEntryLocked(&entry); err != nil {
		return Entry{}, err
	}
	s.storeEntryLocked(entry)
	return cloneEntry(entry), nil
}

func (s *Session) MoveTo(leafID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if leafID != "" {
		if _, ok := s.entries[leafID]; !ok {
			return fmt.Errorf("unknown leaf %q", leafID)
		}
	}
	s.leafID = leafID
	s.updatedAt = time.Now()
	return nil
}

func (s *Session) Fork(newID, leafID string) (*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if leafID == "" {
		leafID = s.leafID
	}
	if leafID != "" {
		if _, ok := s.entries[leafID]; !ok {
			return nil, fmt.Errorf("unknown leaf %q", leafID)
		}
	}
	fork := New(newID)
	fork.cwd = s.cwd
	fork.projectID = s.projectID
	fork.name = s.name
	fork.title = s.title
	fork.createdAt = s.createdAt
	fork.updatedAt = time.Now()
	for _, id := range s.order {
		fork.entries[id] = cloneEntry(s.entries[id])
		fork.order = append(fork.order, id)
	}
	fork.leafID = leafID
	return fork, nil
}

func (s *Session) Children(parentID string) []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	children := make([]Entry, 0)
	for _, id := range s.order {
		entry := s.entries[id]
		if entry.ParentID == parentID {
			children = append(children, cloneEntry(entry))
		}
	}
	return children
}

func (s *Session) Load(entries []Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range entries {
		if err := s.loadEntryLocked(entry); err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) BuildContext() Context {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.buildContextLocked()
}

func (s *Session) append(entry Entry) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.prepareEntryLocked(&entry); err != nil {
		return Entry{}, err
	}
	s.storeEntryLocked(entry)
	return cloneEntry(entry), nil
}

func (s *Session) prepareEntryLocked(entry *Entry) error {
	if entry.Type == "" {
		return errors.New("session entry requires type")
	}
	if entry.ID == "" {
		entry.ID = newEntryID()
	}
	if entry.Version == 0 {
		entry.Version = Version
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}
	if entry.Type != EntrySessionInfo && entry.Type != EntryLeaf && entry.ParentID == "" {
		entry.ParentID = s.leafID
	}
	return entry.Validate()
}

func (s *Session) storeEntryLocked(entry Entry) {
	entry = cloneEntry(entry)
	if _, exists := s.entries[entry.ID]; !exists {
		s.order = append(s.order, entry.ID)
	}
	s.entries[entry.ID] = entry
	if entry.Type == EntrySessionInfo {
		if entry.CWD != "" {
			s.cwd = entry.CWD
		}
		if entry.ProjectID != "" {
			s.projectID = entry.ProjectID
		}
		if entry.Name != "" {
			s.name = entry.Name
		}
		if entry.Title != "" {
			s.title = entry.Title
		}
	} else if entry.Type == EntryLeaf {
		s.leafID = entry.LeafID
	} else {
		s.leafID = entry.ID
	}
	if s.createdAt.IsZero() || entry.Timestamp.Before(s.createdAt) {
		s.createdAt = entry.Timestamp
	}
	if entry.Timestamp.After(s.updatedAt) {
		s.updatedAt = entry.Timestamp
	} else {
		s.updatedAt = time.Now()
	}
}

func (s *Session) messageCountLocked() int {
	count := 0
	for _, entry := range s.entries {
		if entry.Type == EntryMessage || entry.Type == EntryCustomMessage {
			count++
		}
	}
	return count
}

func (s *Session) loadEntryLocked(entry Entry) error {
	if entry.Type == "" {
		return errors.New("session entry requires type")
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}
	if entry.Version == 0 {
		entry.Version = Version
	}
	if entry.Type != EntryLeaf && entry.ID == "" {
		entry.ID = newEntryID()
	}
	if err := entry.Validate(); err != nil {
		return err
	}
	s.storeEntryLocked(entry)
	return nil
}

func (s *Session) buildContextLocked() Context {
	return s.buildContextForLeafLocked(s.leafID)
}

func (s *Session) buildContextForLeafLocked(leafID string) Context {
	path := s.pathToLeafLocked(leafID)
	ctx := Context{LeafID: leafID}
	for _, entry := range path {
		applyStateEntry(&ctx, entry)
	}
	compactionIndex := lastCompactionIndex(path)
	if compactionIndex >= 0 {
		compaction := path[compactionIndex]
		ctx.Messages = append(ctx.Messages, CompactionSummaryMessage(compaction.Summary, compaction.Timestamp))
		for _, entry := range entriesAfterCompaction(path, compactionIndex, compaction.FirstKeptEntryID) {
			appendContextMessage(&ctx, entry)
		}
		return cloneContext(ctx)
	}
	for _, entry := range path {
		appendContextMessage(&ctx, entry)
	}
	return cloneContext(ctx)
}

func (s *Session) pathToLeafLocked(leafID string) []Entry {
	if leafID == "" {
		return nil
	}
	path := []Entry{}
	seen := map[string]bool{}
	currentID := leafID
	for currentID != "" {
		if seen[currentID] {
			break
		}
		seen[currentID] = true
		entry, ok := s.entries[currentID]
		if !ok {
			break
		}
		path = append([]Entry{cloneEntry(entry)}, path...)
		currentID = entry.ParentID
	}
	return path
}

func lastCompactionIndex(path []Entry) int {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i].Type == EntryCompaction {
			return i
		}
	}
	return -1
}

func entriesAfterCompaction(path []Entry, compactionIndex int, firstKeptEntryID string) []Entry {
	entries := []Entry{}
	foundFirstKept := firstKeptEntryID == ""
	for i := 0; i < compactionIndex; i++ {
		entry := path[i]
		if entry.ID == firstKeptEntryID {
			foundFirstKept = true
		}
		if foundFirstKept {
			entries = append(entries, entry)
		}
	}
	entries = append(entries, path[compactionIndex+1:]...)
	return entries
}

func applyStateEntry(ctx *Context, entry Entry) {
	switch entry.Type {
	case EntryMessage:
		switch message := entry.Message.(type) {
		case protocol.AssistantMessage:
			if message.Model != "" {
				ctx.Model = message.Model
			}
			if message.Provider != "" {
				ctx.Provider = message.Provider
			}
		case *protocol.AssistantMessage:
			if message != nil {
				if message.Model != "" {
					ctx.Model = message.Model
				}
				if message.Provider != "" {
					ctx.Provider = message.Provider
				}
			}
		}
	case EntryModelChange:
		ctx.Model = entry.Model
		ctx.Provider = entry.Provider
	case EntryThinkingLevelChange:
		ctx.Reasoning = entry.Reasoning
	case EntryActiveToolsChange:
		ctx.ToolNames = cloneStrings(entry.ToolNames)
	}
}

func appendContextMessage(ctx *Context, entry Entry) {
	switch entry.Type {
	case EntryMessage, EntryCustomMessage:
		if entry.Message != nil {
			ctx.Messages = append(ctx.Messages, protocol.CloneMessage(entry.Message))
		}
	case EntryBranchSummary:
		ctx.Messages = append(ctx.Messages, BranchSummaryMessage(entry.Summary, entry.Timestamp))
	}
}

func CompactionSummaryMessage(summary string, timestamp time.Time) protocol.UserMessage {
	return protocol.UserMessage{
		Content:   protocol.ContentList{protocol.NewTextContent("Context summary:\n\n" + summary)},
		Timestamp: timestamp.UnixMilli(),
	}
}

func BranchSummaryMessage(summary string, timestamp time.Time) protocol.UserMessage {
	return protocol.UserMessage{
		Content:   protocol.ContentList{protocol.NewTextContent("Branch summary:\n\n" + summary)},
		Timestamp: timestamp.UnixMilli(),
	}
}

func cloneContext(ctx Context) Context {
	ctx.Messages = protocol.CloneMessageList(ctx.Messages)
	ctx.ToolNames = cloneStrings(ctx.ToolNames)
	return ctx
}

var (
	sessionIDCounter atomic.Int64
	entryIDCounter   atomic.Int64
	idStart          = time.Now().UnixNano()
)

func newSessionID() string {
	return fmt.Sprintf("session_%x_%x", idStart, sessionIDCounter.Add(1))
}

func newEntryID() string {
	return fmt.Sprintf("entry_%x_%x", idStart, entryIDCounter.Add(1))
}
