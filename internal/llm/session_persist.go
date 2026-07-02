package llm

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"oops/internal/logutil"

	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"
)

// OpenSessionStore 打开（或创建）JSONL-backed SessionStore。
// dir 为 session 文件存放目录，如 "data/sessions"。
// 启动时扫描目录中所有 .jsonl 文件并恢复到热缓存。
func OpenSessionStore(dir string) (*SessionStore, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	onAppend := func(sessionID string, entry *SessionEntry) error {
		return appendToFile(dir, sessionID, entry)
	}

	store := NewPersistentSessionStore(dir, onAppend)

	if err := loadAllSessions(store, dir); err != nil {
		logutil.Warn("session: load existing sessions", zap.Error(err))
	}

	return store, nil
}

// appendToFile 向 session JSONL 文件追加一行 entry。
func appendToFile(dir, sessionID string, entry *SessionEntry) error {
	path := filepath.Join(dir, sessionID+".jsonl")

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}

	return f.Sync()
}

// loadAllSessions 扫描 dir 中所有 .jsonl 文件，恢复到 store 热缓存。
func loadAllSessions(store *SessionStore, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	count := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}

		sessionID := strings.TrimSuffix(e.Name(), ".jsonl")
		path := filepath.Join(dir, e.Name())

		if err := loadSessionFile(store, sessionID, path); err != nil {
			logutil.Warn("session: skip file", zap.String("file", e.Name()), zap.Error(err))
			continue
		}
		count++
	}

	if count > 0 {
		logutil.Infof("session: loaded %d sessions from %s", count, dir)
	}

	return nil
}

// loadSessionFile 解析单个 JSONL 文件，恢复 session 状态。
func loadSessionFile(store *SessionStore, sessionID, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// 先确保 session 存在于 store（用 GetOrCreate，如果已存在则复用）。
	// 为了支持冷启动，直接通过内部 map 创建。
	// 使用 store.Create 会生成新 ID，这里需要手动注入。

	// 首行必须是 session header。
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1 MB buffer

	var projectID string
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var raw struct {
			Type             string          `json:"type"`
			ID               string          `json:"id"`
			ParentID         string          `json:"parentId"`
			Timestamp        json.RawMessage `json:"timestamp"`
			ProjectID        string          `json:"projectId"`
			Message          json.RawMessage `json:"message"`
			Summary          string          `json:"summary"`
			FirstKeptEntryID string          `json:"firstKeptEntryId"`
			TokensBefore     int             `json:"tokensBefore"`
		}

		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			logutil.Warn("session: parse line", zap.String("file", filepath.Base(path)), zap.Int("line", lineNum), zap.Error(err))
			continue
		}

		// 第一行：session header。
		if lineNum == 1 {
			if raw.Type != "session" {
				return nil // 不是合法 session 文件，跳过
			}
			projectID = raw.ProjectID
			// 确保 session 存在。
			store.GetOrCreate(sessionID, projectID)
			continue
		}

		// 后续行：message 或 compaction entry。
		entry := &SessionEntry{
			Type:             EntryType(raw.Type),
			ID:               raw.ID,
			ParentID:         raw.ParentID,
			Summary:          raw.Summary,
			FirstKeptEntryID: raw.FirstKeptEntryID,
			TokensBefore:     raw.TokensBefore,
		}

		// 反序列化 Message。
		if raw.Type == string(EntryMessage) && len(raw.Message) > 0 {
			var msg schemaMsg
			if err := json.Unmarshal(raw.Message, &msg); err == nil {
				entry.Message = msg.toSchemaMessage()
			}
		}

		store.LoadEntry(sessionID, entry)
	}

	return scanner.Err()
}

// schemaMsg 是 JSONL 中 message 的中间表示。
// 避免直接依赖 schema.Message 的 JSON 格式细节。
type schemaMsg struct {
	Role       string          `json:"role"`
	Content    string          `json:"content"`
	ToolCallID string          `json:"toolCallId,omitempty"`
	ToolName   string          `json:"toolName,omitempty"`
	ToolCalls  []schemaToolCall `json:"toolCalls,omitempty"`
}

type schemaToolCall struct {
	ID       string              `json:"id"`
	Function schemaToolCallFunc  `json:"function"`
}

type schemaToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func (m *schemaMsg) toSchemaMessage() *schema.Message {
	msg := &schema.Message{
		Role:       schema.RoleType(m.Role),
		Content:    m.Content,
		ToolCallID: m.ToolCallID,
		ToolName:   m.ToolName,
	}
	for _, tc := range m.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, schema.ToolCall{
			ID: tc.ID,
			Function: schema.FunctionCall{
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
		})
	}
	return msg
}
