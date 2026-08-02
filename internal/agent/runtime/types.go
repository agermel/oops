package runtime

import (
	"context"

	protocol "oops/internal/agent/ai"
	"oops/internal/agent/runtime/compaction"
	"oops/internal/agent/runtime/session"
	agentskills "oops/internal/agent/runtime/skills"
)

type AgentHarnessPhase string

const (
	AgentHarnessPhaseIdle          AgentHarnessPhase = "idle"
	AgentHarnessPhaseTurn          AgentHarnessPhase = "turn"
	AgentHarnessPhaseCompaction    AgentHarnessPhase = "compaction"
	AgentHarnessPhaseBranchSummary AgentHarnessPhase = "branch_summary"
	AgentHarnessPhaseRetry         AgentHarnessPhase = "retry"
)

type SavePointEvent struct {
	Type                string `json:"type"`
	HadPendingMutations bool   `json:"hadPendingMutations"`
}

func (SavePointEvent) EventName() string { return "save_point" }
func (SavePointEvent) runEvent()         {}

type SettledEvent struct {
	Type          string `json:"type"`
	NextTurnCount int    `json:"nextTurnCount"`
}

func (SettledEvent) EventName() string { return "settled" }
func (SettledEvent) runEvent()         {}

type SessionBeforeCompactEvent struct {
	Type               string                 `json:"type"`
	Preparation        compaction.Preparation `json:"preparation"`
	BranchEntries      []session.Entry        `json:"branchEntries"`
	CustomInstructions string                 `json:"customInstructions,omitempty"`
}

func (SessionBeforeCompactEvent) EventName() string { return "session_before_compact" }
func (SessionBeforeCompactEvent) runEvent()         {}

type SessionCompactEvent struct {
	Type            string        `json:"type"`
	CompactionEntry session.Entry `json:"compactionEntry"`
}

func (SessionCompactEvent) EventName() string { return "session_compact" }
func (SessionCompactEvent) runEvent()         {}

type NavigateTreeOptions struct {
	Summarize           bool
	Summary             string
	CustomInstructions  string
	ReplaceInstructions bool
}

type TreePreparation struct {
	TargetID            string          `json:"targetId"`
	OldLeafID           string          `json:"oldLeafId,omitempty"`
	CommonAncestorID    string          `json:"commonAncestorId,omitempty"`
	EntriesToSummarize  []session.Entry `json:"entriesToSummarize"`
	UserWantsSummary    bool            `json:"userWantsSummary"`
	CustomInstructions  string          `json:"customInstructions,omitempty"`
	ReplaceInstructions bool            `json:"replaceInstructions,omitempty"`
}

type SessionBeforeTreeEvent struct {
	Type        string          `json:"type"`
	Preparation TreePreparation `json:"preparation"`
}

func (SessionBeforeTreeEvent) EventName() string { return "session_before_tree" }
func (SessionBeforeTreeEvent) runEvent()         {}

type SessionTreeEvent struct {
	Type         string         `json:"type"`
	NewLeafID    string         `json:"newLeafId,omitempty"`
	OldLeafID    string         `json:"oldLeafId,omitempty"`
	SummaryEntry *session.Entry `json:"summaryEntry,omitempty"`
}

func (SessionTreeEvent) EventName() string { return "session_tree" }
func (SessionTreeEvent) runEvent()         {}

type ModelUpdateEvent struct {
	Type             string `json:"type"`
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	PreviousProvider string `json:"previousProvider,omitempty"`
	PreviousModel    string `json:"previousModel,omitempty"`
	Source           string `json:"source"`
}

func (ModelUpdateEvent) EventName() string { return "model_update" }
func (ModelUpdateEvent) runEvent()         {}

type ThinkingLevelUpdateEvent struct {
	Type          string `json:"type"`
	Level         string `json:"level"`
	PreviousLevel string `json:"previousLevel"`
}

func (ThinkingLevelUpdateEvent) EventName() string { return "thinking_level_update" }
func (ThinkingLevelUpdateEvent) runEvent()         {}

type ToolsUpdateEvent struct {
	Type                    string   `json:"type"`
	ToolNames               []string `json:"toolNames"`
	PreviousToolNames       []string `json:"previousToolNames"`
	ActiveToolNames         []string `json:"activeToolNames"`
	PreviousActiveToolNames []string `json:"previousActiveToolNames"`
	Source                  string   `json:"source"`
}

func (ToolsUpdateEvent) EventName() string { return "tools_update" }
func (ToolsUpdateEvent) runEvent()         {}

type AgentHarnessResources struct {
	Skills          []agentskills.Skill `json:"skills,omitempty"`
	PromptTemplates []PromptTemplate    `json:"promptTemplates,omitempty"`
}

type BeforeAgentStartContext struct {
	Messages     protocol.MessageList
	SystemPrompt string
	Resources    AgentHarnessResources
}

type BeforeAgentStartResult struct {
	Messages     protocol.MessageList
	SystemPrompt *string
}

type BeforeAgentStartHook func(context.Context, BeforeAgentStartContext) (BeforeAgentStartResult, error)

type ResourcesUpdateEvent struct {
	Type              string                `json:"type"`
	Resources         AgentHarnessResources `json:"resources"`
	PreviousResources AgentHarnessResources `json:"previousResources"`
}

func (ResourcesUpdateEvent) EventName() string { return "resources_update" }
func (ResourcesUpdateEvent) runEvent()         {}
