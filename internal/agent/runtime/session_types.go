package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	protocol "oops/internal/agent/ai"
	aiutils "oops/internal/agent/ai/utils"
)

const formatVersion = 1

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
	Version   int       `json:"version"`
	ID        string    `json:"id"`
	ParentID  string    `json:"parentId,omitempty"`
	Timestamp time.Time `json:"timestamp"`

	CWD       string `json:"cwd,omitempty"`
	ProjectID string `json:"projectId,omitempty"`

	Message protocol.AgentMessage `json:"message,omitempty"`

	Model     string   `json:"model,omitempty"`
	Provider  string   `json:"provider,omitempty"`
	Reasoning string   `json:"reasoning,omitempty"`
	ToolNames []string `json:"toolNames,omitempty"`

	Summary          string          `json:"summary,omitempty"`
	FirstKeptEntryID string          `json:"firstKeptEntryId,omitempty"`
	TokensBefore     int             `json:"tokensBefore,omitempty"`
	Details          json.RawMessage `json:"details,omitempty"`

	CustomType string          `json:"customType,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	Label      string          `json:"label,omitempty"`
	Name       string          `json:"name,omitempty"`
	Title      string          `json:"title,omitempty"`
	LeafID     string          `json:"leafId,omitempty"`
}

type entryWire struct {
	Type             EntryType       `json:"type"`
	Version          int             `json:"version"`
	ID               string          `json:"id"`
	ParentID         string          `json:"parentId,omitempty"`
	Timestamp        time.Time       `json:"timestamp"`
	CWD              string          `json:"cwd,omitempty"`
	ProjectID        string          `json:"projectId,omitempty"`
	Message          json.RawMessage `json:"message,omitempty"`
	Model            string          `json:"model,omitempty"`
	Provider         string          `json:"provider,omitempty"`
	Reasoning        string          `json:"reasoning,omitempty"`
	ToolNames        []string        `json:"toolNames,omitempty"`
	Summary          string          `json:"summary,omitempty"`
	FirstKeptEntryID string          `json:"firstKeptEntryId,omitempty"`
	TokensBefore     int             `json:"tokensBefore,omitempty"`
	Details          json.RawMessage `json:"details,omitempty"`
	CustomType       string          `json:"customType,omitempty"`
	Payload          json.RawMessage `json:"payload,omitempty"`
	Label            string          `json:"label,omitempty"`
	Name             string          `json:"name,omitempty"`
	Title            string          `json:"title,omitempty"`
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
		ProjectID:        e.ProjectID,
		Message:          rawMessage,
		Model:            e.Model,
		Provider:         e.Provider,
		Reasoning:        e.Reasoning,
		ToolNames:        cloneStrings(e.ToolNames),
		Summary:          e.Summary,
		FirstKeptEntryID: e.FirstKeptEntryID,
		TokensBefore:     e.TokensBefore,
		Details:          cloneRaw(e.Details),
		CustomType:       e.CustomType,
		Payload:          cloneRaw(e.Payload),
		Label:            e.Label,
		Name:             e.Name,
		Title:            e.Title,
		LeafID:           e.LeafID,
	})
}

func (e *Entry) UnmarshalJSON(data []byte) error {
	var wire entryWire
	if err := aiutils.DecodeStrictJSON(data, &wire); err != nil {
		return err
	}
	if wire.Version != formatVersion {
		return fmt.Errorf("session entry version %d, want %d", wire.Version, formatVersion)
	}
	if err := validateEntryType(wire.Type); err != nil {
		return err
	}
	var message protocol.AgentMessage
	if len(wire.Message) > 0 {
		parsed, err := protocol.UnmarshalMessage(wire.Message)
		if err != nil {
			return err
		}
		message = parsed
	}
	*e = Entry{
		Type:             wire.Type,
		Version:          wire.Version,
		ID:               wire.ID,
		ParentID:         wire.ParentID,
		Timestamp:        wire.Timestamp,
		CWD:              wire.CWD,
		ProjectID:        wire.ProjectID,
		Message:          message,
		Model:            wire.Model,
		Provider:         wire.Provider,
		Reasoning:        wire.Reasoning,
		ToolNames:        cloneStrings(wire.ToolNames),
		Summary:          wire.Summary,
		FirstKeptEntryID: wire.FirstKeptEntryID,
		TokensBefore:     wire.TokensBefore,
		Details:          cloneRaw(wire.Details),
		CustomType:       wire.CustomType,
		Payload:          cloneRaw(wire.Payload),
		Label:            wire.Label,
		Name:             wire.Name,
		Title:            wire.Title,
		LeafID:           wire.LeafID,
	}
	return e.Validate()
}

func (e Entry) Validate() error {
	if err := validateEntryType(e.Type); err != nil {
		return err
	}
	if e.Version != formatVersion {
		return fmt.Errorf("session entry version %d, want %d", e.Version, formatVersion)
	}
	if e.ID == "" {
		return errors.New("session entry requires id")
	}
	if e.Timestamp.IsZero() {
		return errors.New("session entry requires timestamp")
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
	return nil
}

func validateEntryType(entryType EntryType) error {
	if entryType == "" {
		return errors.New("session entry requires type")
	}
	switch entryType {
	case EntrySessionInfo, EntryMessage, EntryModelChange, EntryThinkingLevelChange, EntryActiveToolsChange,
		EntryCompaction, EntryBranchSummary, EntryCustom, EntryCustomMessage, EntryLabel, EntryLeaf:
		return nil
	default:
		return fmt.Errorf("unknown session entry type %q", entryType)
	}
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
	ProjectID string
	Name      string
	Title     string
	Summary   string
	LeafID    string
	CreatedAt time.Time
	UpdatedAt time.Time
	Entries   int
	Messages  int
}

type Storage interface {
	Append(sessionID string, entry Entry) error
	Load(sessionID string) ([]Entry, error)
	List() ([]string, error)
	Delete(sessionID string) (bool, error)
}

func cloneEntry(entry Entry) Entry {
	entry.Message = protocol.CloneMessage(entry.Message)
	entry.ToolNames = cloneStrings(entry.ToolNames)
	entry.Details = cloneRaw(entry.Details)
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
	return slices.Clone(values)
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	return bytes.Clone(raw)
}
