package llm

import (
	"database/sql"
	"encoding/json"
	"sync"
	"time"

	"oops/internal/logutil"

	_ "modernc.org/sqlite"
	"go.uber.org/zap"
)

// EventStore 将 Agent 执行事件持久化到 SQLite。
// 与 SessionStore 配合使用：SessionStore 作为热数据缓存，EventStore 提供持久化和历史搜索。
type EventStore struct {
	db *sql.DB
	mu sync.Mutex // 写操作串行化
}

// storedEvent 是 events 表的一行。
type storedEvent struct {
	RunID      string `json:"run_id"`
	SessionID  string `json:"session_id"`
	ProjectID  string `json:"project_id"`
	Seq        int    `json:"seq"`
	Timestamp  int64  `json:"timestamp"`
	Type       string `json:"type"`
	Content    string `json:"content"`
	ToolName   string `json:"tool_name"`
	ToolArgs   string `json:"tool_args"`
	ToolCallID string `json:"tool_call_id"`
}

// OpenEventStore 打开（或创建）SQLite 事件存储。
func OpenEventStore(path string) (*EventStore, error) {
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}

	// 连接池：SQLite 写操作串行，连接数设为 1。
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	es := &EventStore{db: db}
	if err := es.migrate(); err != nil {
		db.Close()
		return nil, err
	}

	logutil.Info("llm: event store opened", zap.String("path", path))
	return es, nil
}

// migrate 创建表和索引。
func (es *EventStore) migrate() error {
	ddl := `
	CREATE TABLE IF NOT EXISTS events (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		run_id      TEXT NOT NULL,
		session_id  TEXT NOT NULL,
		project_id  TEXT DEFAULT '',
		seq         INTEGER NOT NULL,
		timestamp   INTEGER NOT NULL,
		type        TEXT NOT NULL,
		content     TEXT,
		tool_name   TEXT,
		tool_args   TEXT,
		tool_call_id TEXT
	);
	CREATE INDEX IF NOT EXISTS idx_events_session ON events(session_id, seq);
	CREATE INDEX IF NOT EXISTS idx_events_project ON events(project_id, timestamp);
	`
	_, err := es.db.Exec(ddl)
	return err
}

// AppendEvent 追加一条事件到存储。
func (es *EventStore) AppendEvent(runID, sessionID, projectID string, seq int, evt StepEvent) error {
	es.mu.Lock()
	defer es.mu.Unlock()

	_, err := es.db.Exec(
		`INSERT INTO events (run_id, session_id, project_id, seq, timestamp, type, content, tool_name, tool_args, tool_call_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		runID, sessionID, projectID, seq, time.Now().UnixMilli(),
		evt.Type, evt.Content, evt.ToolName, evt.ToolArgs, evt.ToolCallID,
	)
	return err
}

// GetEvents 按 runID 返回有序事件列表。
func (es *EventStore) GetEvents(runID string) ([]StepEvent, error) {
	rows, err := es.db.Query(
		`SELECT type, content, tool_name, tool_args, tool_call_id
		 FROM events WHERE run_id = ? ORDER BY seq`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []StepEvent
	for rows.Next() {
		var evt StepEvent
		if err := rows.Scan(&evt.Type, &evt.Content, &evt.ToolName, &evt.ToolArgs, &evt.ToolCallID); err != nil {
			return nil, err
		}
		events = append(events, evt)
	}
	return events, rows.Err()
}

// GetSessionMessages 从事件表重建会话消息列表（用于 SessionStore 冷启动恢复）。
func (es *EventStore) GetSessionMessages(sessionID string) ([]storedMessage, error) {
	rows, err := es.db.Query(
		`SELECT type, content, tool_name, tool_call_id, seq, run_id
		 FROM (
		   SELECT type, content, tool_name, tool_call_id, seq, run_id,
		          ROW_NUMBER() OVER (PARTITION BY seq ORDER BY id) AS rn
		   FROM events
		   WHERE session_id = ?
		 )
		 WHERE rn = 1
		 ORDER BY seq`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []storedMessage
	for rows.Next() {
		var sm storedMessage
		if err := rows.Scan(&sm.Role, &sm.Content, &sm.ToolName, &sm.ToolCallID, &sm.Seq, &sm.RunID); err != nil {
			return nil, err
		}
		msgs = append(msgs, sm)
	}
	return msgs, rows.Err()
}

type storedMessage struct {
	Role       string `json:"role"`
	Content    string `json:"content"`
	ToolName   string `json:"tool_name,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	Seq        int    `json:"seq"`
	RunID      string `json:"run_id"`
}

// ListSessions 从事件表按 projectID 聚合会话信息。
func (es *EventStore) ListSessions(projectID string) ([]SessionInfo, error) {
	var rows *sql.Rows
	var err error
	if projectID == "*" {
		rows, err = es.db.Query(
			`SELECT session_id, project_id, COUNT(*) as msg_count, MIN(timestamp), MAX(timestamp)
			 FROM events GROUP BY session_id ORDER BY MAX(timestamp) DESC`)
	} else {
		rows, err = es.db.Query(
			`SELECT session_id, project_id, COUNT(*) as msg_count, MIN(timestamp), MAX(timestamp)
			 FROM events WHERE project_id = ? GROUP BY session_id ORDER BY MAX(timestamp) DESC`, projectID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var infos []SessionInfo
	for rows.Next() {
		var info SessionInfo
		var created, updated int64
		if err := rows.Scan(&info.ID, &info.ProjectID, &info.MessageCount, &created, &updated); err != nil {
			return nil, err
		}
		info.CreatedAt = created
		info.UpdatedAt = updated
		infos = append(infos, info)
	}
	return infos, rows.Err()
}

// SearchMessages 在事件内容中搜索关键词，返回匹配的会话消息。
func (es *EventStore) SearchMessages(query string, limit int) ([]storedMessage, error) {
	rows, err := es.db.Query(
		`SELECT type, content, tool_name, tool_call_id, seq, run_id
		 FROM events WHERE content LIKE ? ORDER BY timestamp DESC LIMIT ?`,
		"%"+query+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []storedMessage
	for rows.Next() {
		var sm storedMessage
		if err := rows.Scan(&sm.Role, &sm.Content, &sm.ToolName, &sm.ToolCallID, &sm.Seq, &sm.RunID); err != nil {
			return nil, err
		}
		msgs = append(msgs, sm)
	}
	return msgs, rows.Err()
}

// Close 关闭数据库连接。
func (es *EventStore) Close() error {
	return es.db.Close()
}

// ---- 从事件恢复 Session ----

// RestoreSession 从 EventStore 恢复指定会话的消息列表。
// 返回可追加到 Session.Messages 的 schema.Message 切片。
func RestoreSession(es *EventStore, sessionID string) ([]storedMessage, error) {
	return es.GetSessionMessages(sessionID)
}

// ToSessionDetail 将存储消息转为 API 可返回的 SessionDetail。
func ToSessionDetail(sessionID, projectID string, msgs []storedMessage) SessionDetail {
	apiMsgs := make([]SessionMessage, 0, len(msgs))
	for _, sm := range msgs {
		apiMsgs = append(apiMsgs, SessionMessage{
			Role:       sm.Role,
			Content:    sm.Content,
			ToolCallID: sm.ToolCallID,
			ToolName:   sm.ToolName,
		})
	}

	createdAt := int64(0)
	updatedAt := int64(0)
	if len(msgs) > 0 {
		createdAt = msgs[0].Timestamp()
		updatedAt = msgs[len(msgs)-1].Timestamp()
	}

	return SessionDetail{
		SessionInfo: SessionInfo{
			ID:           sessionID,
			ProjectID:    projectID,
			MessageCount: len(apiMsgs),
			CreatedAt:    createdAt,
			UpdatedAt:    updatedAt,
		},
		Messages: apiMsgs,
	}
}

// Timestamp 返回存储消息的时间戳（通过 RunID 前缀推断）。
func (sm storedMessage) Timestamp() int64 {
	// RunID 包含时间信息，这里返回 seq * 1000 作为近似。
	return int64(sm.Seq) * 1000
}

// StoredMessageToJSON 将存储消息序列化为 JSON 字符串。
func StoredMessageToJSON(msgs []storedMessage) string {
	data, _ := json.Marshal(msgs)
	return string(data)
}
