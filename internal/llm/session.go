package llm

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"oops/internal/logutil"

	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"
)

// EntryType 区分 Session DAG 中的条目类型。
type EntryType string

const (
	EntryMessage    EntryType = "message"
	EntryCompaction EntryType = "compaction"
)

// SessionEntry 是 session JSONL DAG 中的一个节点。
// 通过 ParentID 形成消息树，支持分支和压缩。
type SessionEntry struct {
	Type      EntryType `json:"type"`
	ID        string    `json:"id"`
	ParentID  string    `json:"parentId"` // 空 = root（session header 之后的第一条消息）
	Timestamp time.Time `json:"timestamp"`

	// Message 专属字段。
	Message *schema.Message `json:"message,omitempty"`

	// Compaction 专属字段。
	Summary          string `json:"summary,omitempty"`
	FirstKeptEntryID string `json:"firstKeptEntryId,omitempty"`
	TokensBefore     int    `json:"tokensBefore,omitempty"`
}

// Session 表示一次对话会话的运行时状态。
// 由后端持有，不同客户端通过 session_id 引用同一会话。
type Session struct {
	ID        string
	ProjectID string // 空 = 全局会话
	Messages  []*schema.Message // 有序历史（system/user/assistant/tool），不含 system prompt
	CreatedAt time.Time
	UpdatedAt time.Time

	// DAG 结构（v2 context 工程）。
	entries map[string]*SessionEntry // entry ID → entry（本 session 范围）
	leafID  string                   // 当前叶节点 ID
}

// SessionInfo 是面向 API 返回的会话概要，不包含完整消息内容。
type SessionInfo struct {
	ID           string `json:"id"`
	ProjectID    string `json:"projectId,omitempty"`
	MessageCount int    `json:"messageCount"`
	CreatedAt    int64  `json:"createdAt"` // unix milli
	UpdatedAt    int64  `json:"updatedAt"` // unix milli
}

// SessionMessage 是面向 API 返回的单条消息。
type SessionMessage struct {
	Role       string `json:"role"` // user / assistant / tool
	Content    string `json:"content"`
	ToolCallID string `json:"toolCallId,omitempty"`
	ToolName   string `json:"toolName,omitempty"`
}

// SessionDetail 是面向 API 返回的会话详情，包含消息列表。
type SessionDetail struct {
	SessionInfo
	Messages []SessionMessage `json:"messages"`
}

// ToDetail 将会话转换为 API 可返回的详情。
func (s *Session) ToDetail() SessionDetail {
	msgs := make([]SessionMessage, 0, len(s.Messages))
	for _, m := range s.Messages {
		sm := SessionMessage{
			Role:    string(m.Role),
			Content: m.Content,
		}
		if m.Role == schema.Tool {
			sm.ToolCallID = m.ToolCallID
			sm.ToolName = m.ToolName
		}
		msgs = append(msgs, sm)
	}
	return SessionDetail{
		SessionInfo: SessionInfo{
			ID:           s.ID,
			ProjectID:    s.ProjectID,
			MessageCount: len(s.Messages),
			CreatedAt:    s.CreatedAt.UnixMilli(),
			UpdatedAt:    s.UpdatedAt.UnixMilli(),
		},
		Messages: msgs,
	}
}

// SessionStore 是会话的内存存储，线程安全。
// v2: 支持 DAG 结构（id/parentId tree）和 JSONL 持久化。
type SessionStore struct {
	mu       sync.RWMutex
	dir      string                    // JSONL 文件目录（空 = 仅内存，不持久化）
	sessions map[string]*Session       // session ID → Session 热缓存
	entries  map[string]*SessionEntry  // 全局 entry ID → SessionEntry（跨 session DAG 引用）
	leafIDs  map[string]string         // session ID → 当前 leaf entry ID

	// 持久化层回调（由 session_persist.go 设置）。
	onAppend func(sessionID string, entry *SessionEntry) error
}

// NewSessionStore 创建一个空的 SessionStore（仅内存模式）。
func NewSessionStore() *SessionStore {
	return &SessionStore{
		sessions: make(map[string]*Session),
		entries:  make(map[string]*SessionEntry),
		leafIDs:  make(map[string]string),
	}
}

// NewPersistentSessionStore 创建带 JSONL 持久化的 SessionStore。
// 由 session_persist.go 调用。
func NewPersistentSessionStore(dir string, onAppend func(string, *SessionEntry) error) *SessionStore {
	return &SessionStore{
		dir:      dir,
		sessions: make(map[string]*Session),
		entries:  make(map[string]*SessionEntry),
		leafIDs:  make(map[string]string),
		onAppend: onAppend,
	}
}

// Create 创建新会话并返回。
func (s *SessionStore) Create(projectID string) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	sess := &Session{
		ID:        newSessionID(),
		ProjectID: projectID,
		CreatedAt: now,
		UpdatedAt: now,
		entries:   make(map[string]*SessionEntry),
	}
	s.sessions[sess.ID] = sess
	return sess
}

// Get 按 ID 获取会话，不存在时返回 nil, false。
func (s *SessionStore) Get(id string) (*Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sess, ok := s.sessions[id]
	return sess, ok
}

// GetOrCreate 按 ID 获取会话；若不存在则创建一个新会话（使用给定 projectID）。
func (s *SessionStore) GetOrCreate(id, projectID string) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sess, ok := s.sessions[id]; ok {
		return sess
	}
	now := time.Now()
	sess := &Session{
		ID:        id,
		ProjectID: projectID,
		CreatedAt: now,
		UpdatedAt: now,
		entries:   make(map[string]*SessionEntry),
	}
	s.sessions[id] = sess
	return sess
}

// AppendMessage 向指定会话追加一条消息。
// 若会话不存在，此调用是安全的 no-op。
func (s *SessionStore) AppendMessage(sessionID string, msg *schema.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		return
	}
	sess.Messages = append(sess.Messages, msg)
	sess.UpdatedAt = time.Now()

	// 创建 DAG entry。
	entry := &SessionEntry{
		Type:      EntryMessage,
		ID:        newEntryID(),
		ParentID:  s.leafIDs[sessionID],
		Timestamp: time.Now(),
		Message:   msg,
	}
	sess.entries[entry.ID] = entry
	s.entries[entry.ID] = entry
	s.leafIDs[sessionID] = entry.ID

	// 持久化。
	if s.onAppend != nil {
		if err := s.onAppend(sessionID, entry); err != nil {
			logutil.Warn("session: persist message", zap.String("session", sessionID), zap.Error(err))
		}
	}
}

// AppendMessages 向指定会话批量追加消息。
// 若会话不存在，此调用是安全的 no-op。
func (s *SessionStore) AppendMessages(sessionID string, msgs []*schema.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		return
	}
	sess.Messages = append(sess.Messages, msgs...)
	sess.UpdatedAt = time.Now()

	for _, msg := range msgs {
		entry := &SessionEntry{
			Type:      EntryMessage,
			ID:        newEntryID(),
			ParentID:  s.leafIDs[sessionID],
			Timestamp: time.Now(),
			Message:   msg,
		}
		sess.entries[entry.ID] = entry
		s.entries[entry.ID] = entry
		s.leafIDs[sessionID] = entry.ID

		if s.onAppend != nil {
			if err := s.onAppend(sessionID, entry); err != nil {
				logutil.Warn("session: persist message", zap.String("session", sessionID), zap.Error(err))
			}
		}
	}
}

// AppendCompaction 向指定会话追加一条压缩条目。
func (s *SessionStore) AppendCompaction(sessionID string, entry *SessionEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		return
	}

	entry.Type = EntryCompaction
	if entry.ID == "" {
		entry.ID = newEntryID()
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}
	entry.ParentID = s.leafIDs[sessionID]

	sess.entries[entry.ID] = entry
	s.entries[entry.ID] = entry
	s.leafIDs[sessionID] = entry.ID
	sess.UpdatedAt = time.Now()

	if s.onAppend != nil {
		if err := s.onAppend(sessionID, entry); err != nil {
			logutil.Warn("session: persist compaction", zap.String("session", sessionID), zap.Error(err))
		}
	}
}

// List 返回指定 projectID 的会话概要列表。
// projectID 为空时返回全局会话；为 "*" 时返回所有会话。
func (s *SessionStore) List(projectID string) []SessionInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var infos []SessionInfo
	for _, sess := range s.sessions {
		if projectID != "*" && sess.ProjectID != projectID {
			continue
		}
		infos = append(infos, SessionInfo{
			ID:           sess.ID,
			ProjectID:    sess.ProjectID,
			MessageCount: len(sess.Messages),
			CreatedAt:    sess.CreatedAt.UnixMilli(),
			UpdatedAt:    sess.UpdatedAt.UnixMilli(),
		})
	}
	return infos
}

// Delete 删除指定会话，返回是否成功删除。
func (s *SessionStore) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[id]
	if !ok {
		return false
	}

	// 清理 entries。
	for entryID := range sess.entries {
		delete(s.entries, entryID)
	}
	delete(s.leafIDs, id)
	delete(s.sessions, id)

	return true
}

// ---- DAG 导航 ----

// pathToLeaf 返回从 root 到 leaf 的 entry 路径（不含 session header）。
// 返回值按时间顺序排列（最早在前）。
func (s *SessionStore) pathToLeaf(sessionID string) []*SessionEntry {
	leafID := s.leafIDs[sessionID]
	if leafID == "" {
		return nil
	}

	var path []*SessionEntry
	current := s.entries[leafID]
	for current != nil {
		path = append([]*SessionEntry{current}, path...) // prepend
		if current.ParentID == "" {
			break
		}
		current = s.entries[current.ParentID]
	}
	return path
}

// findLastCompaction 在路径中从后向前找到最新的 compaction entry。
func findLastCompaction(path []*SessionEntry) int {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i].Type == EntryCompaction {
			return i
		}
	}
	return -1
}

// BuildContext 从 session DAG 重建消息列表。
// BuildContext 算法：
//  1. 若存在 compaction → 注入 [摘要] 合成消息 → 保留 firstKept → 追加 compaction 后消息
//  2. 若不存在 compaction → 返回所有消息
func (s *SessionStore) BuildContext(sessionID string) []*schema.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.buildContextLocked(sessionID)
}

func (s *SessionStore) buildContextLocked(sessionID string) []*schema.Message {
	path := s.pathToLeaf(sessionID)
	compIdx := findLastCompaction(path)

	if compIdx == -1 {
		var msgs []*schema.Message
		for _, e := range path {
			if e.Type == EntryMessage && e.Message != nil {
				msgs = append(msgs, e.Message)
			}
		}
		return msgs
	}

	compaction := path[compIdx]
	var msgs []*schema.Message

	// 注入摘要合成消息。
	summaryContent := "以下是旧上下文摘要。后续回答必须参考它，但最近消息优先级更高。\n\n" + compaction.Summary
	msgs = append(msgs, schema.UserMessage(summaryContent))

	// 保留 firstKeptEntryId 到 compaction 之间的原始消息。
	foundFirstKept := false
	for i := 0; i < compIdx; i++ {
		entry := path[i]
		if entry.ID == compaction.FirstKeptEntryID {
			foundFirstKept = true
		}
		if foundFirstKept && entry.Type == EntryMessage && entry.Message != nil {
			msgs = append(msgs, entry.Message)
		}
	}

	// 追加 compaction 之后的所有消息。
	for i := compIdx + 1; i < len(path); i++ {
		entry := path[i]
		if entry.Type == EntryMessage && entry.Message != nil {
			msgs = append(msgs, entry.Message)
		}
	}

	return msgs
}

// CompactIfNeeded 检查是否需要压缩，若需要则创建压缩条目（仅构造对象，不修改 store）。
// maxApproxTokens: 触发压缩的 token 阈值。
// keepRecentMessages: 始终保留的最近消息数。
// 返回 nil 表示无需压缩。
// 调用方负责通过 AppendCompaction 将返回的 entry 持久化到 store。
//
// 摘要策略：默认使用确定性拼接（role:text），调用方可通过外部 LLM 调用替换摘要内容。
func (s *SessionStore) CompactIfNeeded(sessionID string, maxApproxTokens int, keepRecentMessages int) *SessionEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path := s.pathToLeaf(sessionID)
	msgEntries := make([]*SessionEntry, 0, len(path))
	for _, e := range path {
		if e.Type == EntryMessage {
			msgEntries = append(msgEntries, e)
		}
	}

	// 构建当前上下文并估算 token。
	currentMsgs := s.buildContextLocked(sessionID)
	tokensBefore := EstimateTokens(currentMsgs)

	if tokensBefore <= maxApproxTokens || len(msgEntries) <= keepRecentMessages {
		return nil
	}

	if len(msgEntries) <= keepRecentMessages {
		return nil
	}

	summarized := msgEntries[:len(msgEntries)-keepRecentMessages]
	kept := msgEntries[len(msgEntries)-keepRecentMessages:]

	summary := summarizeEntries(summarized)
	firstKeptEntryID := kept[0].ID

	return &SessionEntry{
		Type:             EntryCompaction,
		ID:               newEntryID(),
		Timestamp:        time.Now(),
		Summary:          summary,
		FirstKeptEntryID: firstKeptEntryID,
		TokensBefore:     tokensBefore,
	}
}

// LoadEntry 从持久化加载 entry 到热缓存（由 session_persist.go 调用）。
func (s *SessionStore) LoadEntry(sessionID string, entry *SessionEntry) {
	sess, ok := s.sessions[sessionID]
	if !ok {
		return
	}
	sess.entries[entry.ID] = entry
	s.entries[entry.ID] = entry
	s.leafIDs[sessionID] = entry.ID

	// 同步 Messages 切片（兼容旧代码）。
	if entry.Type == EntryMessage && entry.Message != nil {
		sess.Messages = append(sess.Messages, entry.Message)
	}
	sess.UpdatedAt = entry.Timestamp
}

// SessionCount 返回当前会话数量（测试用）。
func (s *SessionStore) SessionCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.sessions)
}

// summarizeEntries 确定性摘要：将消息角色和内容拼接。
// 若 llmClient 可用，调用方可在外部用 LLM 替换摘要内容。
func summarizeEntries(entries []*SessionEntry) string {
	var lines []string
	for _, e := range entries {
		if e.Message == nil {
			continue
		}
		role := string(e.Message.Role)
		content := e.Message.Content
		// 截断过长内容。
		if len([]rune(content)) > 300 {
			content = string([]rune(content)[:300]) + "..."
		}
		lines = append(lines, fmt.Sprintf("%s: %s", role, content))
	}
	return joinLines(lines, "\n")
}

func joinLines(lines []string, sep string) string {
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(lines[0])
	for _, line := range lines[1:] {
		b.WriteString(sep)
		b.WriteString(line)
	}
	return b.String()
}

var (
	sessionIDSeq   int64
	sessionIDStart = time.Now().UnixNano()
	entryIDSeq     int64
	entryIDStart   = time.Now().UnixNano()
)

// newSessionID 生成一个新的会话 ID。
func newSessionID() string {
	seq := sessionIDSeq
	sessionIDSeq++
	return fmt.Sprintf("sess_%x_%x", sessionIDStart, seq)
}

// newEntryID 生成一个新的 entry ID。
func newEntryID() string {
	seq := entryIDSeq
	entryIDSeq++
	return fmt.Sprintf("entry_%x_%x", entryIDStart, seq)
}
