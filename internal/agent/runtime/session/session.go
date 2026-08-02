package session

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	protocol "oops/internal/agent/ai"

	"github.com/google/uuid"
)

type Session struct {
	mu sync.RWMutex

	id         string
	cwd        string
	projectID  string
	name       string
	title      string
	entries    map[string]Entry
	order      []string
	labelsByID map[string]string
	leafID     string
	createdAt  time.Time
	updatedAt  time.Time
}

func New(id string) *Session {
	if id == "" {
		id = newSessionID()
	}
	now := time.Now()
	return &Session{
		id:         id,
		entries:    make(map[string]Entry),
		labelsByID: make(map[string]string),
		createdAt:  now,
		updatedAt:  now,
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
	if user, ok := protocol.AsUserMessage(message); ok {
		content = user.Content
	} else if assistant, ok := protocol.AsAssistantMessage(message); ok {
		content = assistant.Content
	} else if result, ok := protocol.AsToolResultMessage(message); ok {
		content = result.Content
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

func (s *Session) Label(targetID string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	label, ok := s.labelsByID[targetID]
	return label, ok
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

func (s *Session) AppendLabel(targetID, label string) (Entry, error) {
	return s.append(Entry{Type: EntryLabel, TargetID: targetID, Label: label})
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

func (s *Session) Fork(options ForkOptions) (*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	leafID := s.leafID
	var forkAtLeaf *Entry
	if options.EntryID != "" {
		target, ok := s.entries[options.EntryID]
		if !ok {
			return nil, fmt.Errorf("invalid fork target: entry %q not found", options.EntryID)
		}
		position := options.Position
		if position == "" {
			position = ForkBefore
		}
		switch position {
		case ForkAt:
			leafID = target.ID
			if target.Type == EntryLeaf {
				leafEntry := cloneEntry(target)
				leafEntry.ParentID = target.LeafID
				forkAtLeaf = &leafEntry
				leafID = target.LeafID
			}
		case ForkBefore:
			if target.Type != EntryMessage || target.Message == nil || target.Message.MessageRole() != protocol.RoleUser {
				return nil, fmt.Errorf("invalid fork target: entry %q is not a user message", options.EntryID)
			}
			leafID = target.ParentID
		default:
			return nil, fmt.Errorf("invalid fork position %q", position)
		}
	}

	fork := New(options.ID)
	fork.cwd = s.cwd
	fork.projectID = s.projectID
	fork.name = s.name
	fork.title = s.title
	for _, entry := range s.pathToLeafLocked(leafID) {
		fork.storeEntryLocked(entry)
	}
	if forkAtLeaf != nil {
		fork.storeEntryLocked(*forkAtLeaf)
	}
	if fork.cwd != "" || fork.projectID != "" || fork.name != "" || fork.title != "" {
		metadata := Entry{
			Type:      EntrySessionInfo,
			CWD:       fork.cwd,
			ProjectID: fork.projectID,
			Name:      fork.name,
			Title:     fork.title,
		}
		if err := fork.prepareEntryLocked(&metadata); err != nil {
			return nil, err
		}
		fork.storeEntryLocked(metadata)
		// Persist detached session metadata before the copied conversation path.
		copy(fork.order[1:], fork.order[:len(fork.order)-1])
		fork.order[0] = metadata.ID
	}
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
	s.mu.RLock()
	id := s.id
	s.mu.RUnlock()

	// 先在候选 Session 完成全部校验，成功后才交换状态。
	// JSONL 的任意一行损坏都不能污染正在服务的会话。
	candidate := &Session{
		id:         id,
		entries:    make(map[string]Entry),
		labelsByID: make(map[string]string),
	}
	for _, entry := range entries {
		if err := candidate.loadEntryLocked(entry); err != nil {
			return err
		}
	}
	if err := candidate.validateLoadedHistoryLocked(); err != nil {
		return err
	}
	candidate.leafID = candidate.executableLeafLocked(candidate.leafID)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.publishLocked(candidate)
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
		entry.ID = newEntryID(s.entries)
	}
	if entry.Version == 0 {
		entry.Version = formatVersion
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}
	if entry.Type != EntrySessionInfo && entry.Type != EntryLeaf && entry.ParentID == "" {
		entry.ParentID = s.leafID
	}
	if err := entry.Validate(); err != nil {
		return err
	}
	if entry.Type == EntryLabel {
		if _, ok := s.entries[entry.TargetID]; !ok {
			return fmt.Errorf("label target %q is not in session", entry.TargetID)
		}
	}
	return s.validateCompactionReferenceLocked(*entry)
}

func (s *Session) storeEntryLocked(entry Entry) {
	entry = cloneEntry(entry)
	if _, exists := s.entries[entry.ID]; !exists {
		s.order = append(s.order, entry.ID)
	}
	s.entries[entry.ID] = entry
	if entry.Type == EntryLabel {
		if s.labelsByID == nil {
			s.labelsByID = make(map[string]string)
		}
		if label := strings.TrimSpace(entry.Label); label != "" {
			s.labelsByID[entry.TargetID] = label
		} else {
			delete(s.labelsByID, entry.TargetID)
		}
	}
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
	if s.updatedAt.IsZero() || entry.Timestamp.After(s.updatedAt) {
		s.updatedAt = entry.Timestamp
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
	if err := entry.Validate(); err != nil {
		return err
	}
	if _, exists := s.entries[entry.ID]; exists {
		return fmt.Errorf("duplicate session entry id %q", entry.ID)
	}
	if entry.ParentID != "" {
		if _, ok := s.entries[entry.ParentID]; !ok {
			return fmt.Errorf("session entry %q references unknown parent %q", entry.ID, entry.ParentID)
		}
	}
	if entry.Type == EntryLeaf && entry.LeafID != "" {
		if _, ok := s.entries[entry.LeafID]; !ok {
			return fmt.Errorf("session leaf %q references unknown entry %q", entry.ID, entry.LeafID)
		}
	}
	if err := s.validateCompactionReferenceLocked(entry); err != nil {
		return err
	}
	s.storeEntryLocked(entry)
	return nil
}

func (s *Session) validateCompactionReferenceLocked(entry Entry) error {
	if entry.Type != EntryCompaction {
		return nil
	}
	if _, ok := s.entries[entry.FirstKeptEntryID]; !ok {
		return fmt.Errorf("compaction first kept entry %q is not in session", entry.FirstKeptEntryID)
	}
	if indexEntry(s.pathToLeafLocked(entry.ParentID), entry.FirstKeptEntryID) < 0 {
		return fmt.Errorf("compaction first kept entry %q is not on parent path", entry.FirstKeptEntryID)
	}
	return nil
}

func (s *Session) buildContextLocked() Context {
	return s.buildContextForLeafLocked(s.executableLeafLocked(s.leafID))
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

func (s *Session) validateLoadedHistoryLocked() error {
	targets := map[string]bool{}
	childCount := map[string]int{}
	for _, id := range s.order {
		entry := s.entries[id]
		if entry.ParentID != "" {
			childCount[entry.ParentID]++
		}
		if entry.Type == EntryLeaf && entry.LeafID != "" {
			targets[entry.LeafID] = true
		}
	}
	for _, id := range s.order {
		if childCount[id] == 0 {
			targets[id] = true
		}
	}
	for target := range targets {
		analysis, _ := s.analyzeLeafLocked(target)
		if analysis.status == messageSequenceInvalid {
			return analysis.err
		}
	}
	return nil
}

func (s *Session) executableLeafLocked(leafID string) string {
	analysis, entryIDs := s.analyzeLeafLocked(leafID)
	if analysis.status != messageSequenceIncomplete {
		return leafID
	}
	if analysis.pendingAssistantIndex < 0 || analysis.pendingAssistantIndex >= len(entryIDs) {
		return s.safeBoundaryLocked(leafID)
	}
	assistant, ok := s.entries[entryIDs[analysis.pendingAssistantIndex]]
	if !ok {
		return s.safeBoundaryLocked(leafID)
	}
	return assistant.ParentID
}

func (s *Session) analyzeLeafLocked(leafID string) (messageSequenceAnalysis, []string) {
	messages, entryIDs := s.contextMessagesForLeafLocked(leafID)
	return analyzeMessageSequence(messages), entryIDs
}

func (s *Session) contextMessagesForLeafLocked(leafID string) (protocol.MessageList, []string) {
	path := s.pathToLeafLocked(leafID)
	messages := protocol.MessageList{}
	entryIDs := []string{}
	appendEntry := func(entry Entry, message protocol.AgentMessage) {
		if message == nil {
			return
		}
		messages = append(messages, protocol.CloneMessage(message))
		entryIDs = append(entryIDs, entry.ID)
	}
	compactionIndex := lastCompactionIndex(path)
	if compactionIndex >= 0 {
		compaction := path[compactionIndex]
		appendEntry(compaction, CompactionSummaryMessage(compaction.Summary, compaction.Timestamp))
		for _, entry := range entriesAfterCompaction(path, compactionIndex, compaction.FirstKeptEntryID) {
			appendContextEntryMessage(entry, appendEntry)
		}
		return messages, entryIDs
	}
	for _, entry := range path {
		appendContextEntryMessage(entry, appendEntry)
	}
	return messages, entryIDs
}

func appendContextEntryMessage(entry Entry, appendEntry func(Entry, protocol.AgentMessage)) {
	switch entry.Type {
	case EntryMessage, EntryCustomMessage:
		appendEntry(entry, entry.Message)
	case EntryBranchSummary:
		appendEntry(entry, BranchSummaryMessage(entry.Summary, entry.Timestamp))
	}
}

func (s *Session) cloneLocked() *Session {
	clone := &Session{
		id:         s.id,
		cwd:        s.cwd,
		projectID:  s.projectID,
		name:       s.name,
		title:      s.title,
		entries:    make(map[string]Entry, len(s.entries)),
		order:      slices.Clone(s.order),
		labelsByID: make(map[string]string, len(s.labelsByID)),
		leafID:     s.leafID,
		createdAt:  s.createdAt,
		updatedAt:  s.updatedAt,
	}
	for id, entry := range s.entries {
		clone.entries[id] = cloneEntry(entry)
	}
	for id, label := range s.labelsByID {
		clone.labelsByID[id] = label
	}
	return clone
}

func (s *Session) publishLocked(candidate *Session) {
	s.cwd = candidate.cwd
	s.projectID = candidate.projectID
	s.name = candidate.name
	s.title = candidate.title
	s.entries = candidate.entries
	s.order = candidate.order
	s.labelsByID = candidate.labelsByID
	s.leafID = candidate.leafID
	s.createdAt = candidate.createdAt
	s.updatedAt = candidate.updatedAt
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
		if message, ok := protocol.AsAssistantMessage(entry.Message); ok {
			if message.Model != "" {
				ctx.Model = message.Model
			}
			if message.Provider != "" {
				ctx.Provider = message.Provider
			}
		}
	case EntryModelChange:
		ctx.Model = entry.Model
		ctx.Provider = entry.Provider
	case EntryThinkingLevelChange:
		ctx.Reasoning = entry.Reasoning
	case EntryActiveToolsChange:
		ctx.ToolNames = cloneStrings(entry.ToolNames)
		ctx.ToolNamesSet = true
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

func cloneContext(ctx Context) Context {
	ctx.Messages = protocol.CloneMessageList(ctx.Messages)
	ctx.ToolNames = cloneStrings(ctx.ToolNames)
	return ctx
}

func newSessionID() string {
	return newUUIDv7()
}

func newEntryID(entries map[string]Entry) string {
	return generateEntryID(entries, newUUIDv7)
}

func generateEntryID(entries map[string]Entry, generate func() string) string {
	for range 100 {
		id := generate()[:8]
		if _, exists := entries[id]; !exists {
			return id
		}
	}
	return generate()
}

func newUUIDv7() string {
	return uuid.Must(uuid.NewV7()).String()
}
