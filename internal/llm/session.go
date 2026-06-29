package llm

import (
	"fmt"
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"
)

// Session 表示一次对话会话的运行时状态。
// 由后端持有，不同客户端通过 session_id 引用同一会话。
type Session struct {
	ID        string
	ProjectID string            // 空 = 全局会话
	Messages  []*schema.Message // 有序历史（system/user/assistant/tool），不含 system prompt
	CreatedAt time.Time
	UpdatedAt time.Time
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
	Role       string `json:"role"`                 // user / assistant / tool
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
type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

// NewSessionStore 创建一个空的 SessionStore。
func NewSessionStore() *SessionStore {
	return &SessionStore{
		sessions: make(map[string]*Session),
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

	_, ok := s.sessions[id]
	if ok {
		delete(s.sessions, id)
	}
	return ok
}

var (
	sessionIDSeq   int64
	sessionIDStart = time.Now().UnixNano()
)

// newSessionID 生成一个新的会话 ID。
func newSessionID() string {
	seq := sessionIDSeq
	sessionIDSeq++
	return fmt.Sprintf("sess_%x_%x", sessionIDStart, seq)
}
