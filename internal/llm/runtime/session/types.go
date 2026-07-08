package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"oops/internal/llm/ai/protocol"
)

const Version = 3

type EntryType string

const (
	EntrySessionInfo         EntryType = "session_info"
	EntryMessage             EntryType = "message"
	EntryModelChange         EntryType = "model_change"
	EntryThinkingLevelChange EntryType = "thinking_level_change"
	EntryActiveToolsChange   EntryType = "active_tools_change"
	EntryCompaction          EntryType = "compaction"
	EntryBranchSummary       EntryType = "branch_summary"
	EntryCustom              EntryType = "custom"
	EntryCustomMessage       EntryType = "custom_message"
	EntryLabel               EntryType = "label"
	EntryLeaf                EntryType = "leaf"
)

type Entry struct {
	Type      EntryType `json:"type"`
	Version   int       `json:"version,omitempty"`
	ID        string    `json:"id"`
	ParentID  string    `json:"parentId,omitempty"`
	Timestamp time.Time `json:"timestamp"`

	CWD string `json:"cwd,omitempty"`

	Message protocol.AgentMessage `json:"message,omitempty"`

	Model     string   `json:"model,omitempty"`
	Provider  string   `json:"provider,omitempty"`
	Reasoning string   `json:"reasoning,omitempty"`
	ToolNames []string `json:"toolNames,omitempty"`

	Summary          string `json:"summary,omitempty"`
	FirstKeptEntryID string `json:"firstKeptEntryId,omitempty"`
	TokensBefore     int    `json:"tokensBefore,omitempty"`

	CustomType string          `json:"customType,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	Label      string          `json:"label,omitempty"`
	Name       string          `json:"name,omitempty"`
	LeafID     string          `json:"leafId,omitempty"`
}

type entryWire struct {
	Type             EntryType       `json:"type"`
	Version          int             `json:"version,omitempty"`
	ID               string          `json:"id"`
	ParentID         string          `json:"parentId,omitempty"`
	Timestamp        time.Time       `json:"timestamp"`
	CWD              string          `json:"cwd,omitempty"`
	Message          json.RawMessage `json:"message,omitempty"`
	Model            string          `json:"model,omitempty"`
	Provider         string          `json:"provider,omitempty"`
	Reasoning        string          `json:"reasoning,omitempty"`
	ToolNames        []string        `json:"toolNames,omitempty"`
	Summary          string          `json:"summary,omitempty"`
	FirstKeptEntryID string          `json:"firstKeptEntryId,omitempty"`
	TokensBefore     int             `json:"tokensBefore,omitempty"`
	CustomType       string          `json:"customType,omitempty"`
	Payload          json.RawMessage `json:"payload,omitempty"`
	Label            string          `json:"label,omitempty"`
	Name             string          `json:"name,omitempty"`
	LeafID           string          `json:"leafId,omitempty"`
}

func (e Entry) MarshalJSON() ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	var rawMessage json.RawMessage
	if e.Message != nil {
		data, err := json.Marshal(e.Message)
		if err != nil {
			return nil, err
		}
		rawMessage = data
	}
	return json.Marshal(entryWire{
		Type:             e.Type,
		Version:          e.Version,
		ID:               e.ID,
		ParentID:         e.ParentID,
		Timestamp:        e.Timestamp,
		CWD:              e.CWD,
		Message:          rawMessage,
		Model:            e.Model,
		Provider:         e.Provider,
		Reasoning:        e.Reasoning,
		ToolNames:        cloneStrings(e.ToolNames),
		Summary:          e.Summary,
		FirstKeptEntryID: e.FirstKeptEntryID,
		TokensBefore:     e.TokensBefore,
		CustomType:       e.CustomType,
		Payload:          cloneRaw(e.Payload),
		Label:            e.Label,
		Name:             e.Name,
		LeafID:           e.LeafID,
	})
}

func (e *Entry) UnmarshalJSON(data []byte) error {
	var wire entryWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	var message protocol.AgentMessage
	if len(wire.Message) > 0 {
		parsed, err := unmarshalEntryMessage(wire.Message)
		if err != nil {
			return err
		}
		message = parsed
	}
	*e = Entry{
		Type:             normalizeEntryType(wire.Type),
		Version:          wire.Version,
		ID:               wire.ID,
		ParentID:         wire.ParentID,
		Timestamp:        wire.Timestamp,
		CWD:              wire.CWD,
		Message:          message,
		Model:            wire.Model,
		Provider:         wire.Provider,
		Reasoning:        wire.Reasoning,
		ToolNames:        cloneStrings(wire.ToolNames),
		Summary:          wire.Summary,
		FirstKeptEntryID: wire.FirstKeptEntryID,
		TokensBefore:     wire.TokensBefore,
		CustomType:       wire.CustomType,
		Payload:          cloneRaw(wire.Payload),
		Label:            wire.Label,
		Name:             wire.Name,
		LeafID:           wire.LeafID,
	}
	return e.Validate()
}

func (e Entry) Validate() error {
	if e.Type == "" {
		return errors.New("session entry requires type")
	}
	if e.ID == "" && e.Type != EntryLeaf && e.Type != EntrySessionInfo {
		return errors.New("session entry requires id")
	}
	switch e.Type {
	case EntrySessionInfo, EntryMessage, EntryModelChange, EntryThinkingLevelChange, EntryActiveToolsChange,
		EntryCompaction, EntryBranchSummary, EntryCustom, EntryCustomMessage, EntryLabel, EntryLeaf:
	default:
		return fmt.Errorf("unknown session entry type %q", e.Type)
	}
	if e.Message != nil {
		if err := e.Message.Validate(); err != nil {
			return err
		}
	}
	if e.Type == EntryMessage && e.Message == nil {
		return errors.New("message entry requires message")
	}
	if e.Type == EntryCustomMessage && e.Message == nil {
		return errors.New("custom message entry requires message")
	}
	if e.Type == EntryCompaction && e.Summary == "" {
		return errors.New("compaction entry requires summary")
	}
	if e.Type == EntryBranchSummary && e.Summary == "" {
		return errors.New("branch summary entry requires summary")
	}
	if e.Type == EntryLeaf && e.LeafID == "" {
		return errors.New("leaf entry requires leafId")
	}
	return nil
}

type Context struct {
	Messages  protocol.MessageList
	Model     string
	Provider  string
	Reasoning string
	ToolNames []string
	LeafID    string
}

type Info struct {
	ID        string
	CWD       string
	Name      string
	LeafID    string
	CreatedAt time.Time
	UpdatedAt time.Time
	Entries   int
}

type Storage interface {
	Append(sessionID string, entry Entry) error
	Load(sessionID string) ([]Entry, error)
	List() ([]string, error)
}

func normalizeEntryType(entryType EntryType) EntryType {
	if entryType == "session" {
		return EntrySessionInfo
	}
	return entryType
}

func cloneEntry(entry Entry) Entry {
	entry.Message = protocol.CloneMessage(entry.Message)
	entry.ToolNames = cloneStrings(entry.ToolNames)
	entry.Payload = cloneRaw(entry.Payload)
	return entry
}

func cloneEntries(entries []Entry) []Entry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]Entry, len(entries))
	for i, entry := range entries {
		out[i] = cloneEntry(entry)
	}
	return out
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out
}
