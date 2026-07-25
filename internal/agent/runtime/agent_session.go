package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"

	protocol "oops/internal/agent/ai"
	agentcore "oops/internal/agent/core"
)

type AgentSessionOptions struct {
	Session         *Session
	Repo            *Repository
	Resources       ResourceSnapshot
	Config          agentcore.AgentLoopConfig
	Model           string
	Provider        string
	Reasoning       string
	ActiveToolNames []string
}

type AgentSession struct {
	mu sync.Mutex

	session *Session
	repo    *Repository

	resources       ResourceSnapshot
	allTools        map[string]agentcore.Tool
	activeToolNames []string

	config    agentcore.AgentLoopConfig
	model     string
	provider  string
	reasoning string

	agent *agentcore.Agent

	events []protocol.AgentEvent
}

type SessionSnapshot struct {
	SessionID  string                    `json:"sessionId"`
	LeafID     string                    `json:"leafId,omitempty"`
	EditorText string                    `json:"editorText,omitempty"`
	Messages   protocol.MessageList      `json:"messages"`
	Events     []protocol.AgentEvent     `json:"events"`
	Tools      []protocol.ToolDefinition `json:"tools"`
	Entries    []Entry                   `json:"entries"`
}

func ClearSnapshotPayload(snapshot SessionSnapshot) SessionSnapshot {
	snapshot.EditorText = ""
	snapshot.Messages = protocol.MessageList{}
	snapshot.Events = []protocol.AgentEvent{}
	snapshot.Tools = []protocol.ToolDefinition{}
	snapshot.Entries = []Entry{}
	return snapshot
}

func NewAgentSession(options AgentSessionOptions) (*AgentSession, error) {
	if options.Session == nil {
		return nil, errors.New("agent session requires session")
	}
	as := &AgentSession{
		session:         options.Session,
		repo:            options.Repo,
		resources:       cloneResourceSnapshot(options.Resources),
		config:          options.Config,
		model:           options.Model,
		provider:        options.Provider,
		reasoning:       options.Reasoning,
		activeToolNames: cloneStrings(options.ActiveToolNames),
	}
	if as.repo != nil {
		if _, ok := as.repo.Get(as.session.ID()); !ok {
			as.repo.Put(as.session)
		}
	}
	if err := as.indexToolsLocked(); err != nil {
		return nil, err
	}
	if err := as.rebuildAgentLocked(); err != nil {
		return nil, err
	}
	return as, nil
}

func (s *AgentSession) ValidateProviderContext() error {
	return s.session.ValidateContext()
}

func (s *AgentSession) Listen(listener agentcore.AgentListener) agentcore.Unsubscribe {
	s.mu.Lock()
	agent := s.agent
	s.mu.Unlock()
	return agent.Listen(listener)
}

func (s *AgentSession) Snapshot() SessionSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked("")
}

func (s *AgentSession) snapshotLocked(editorText string) SessionSnapshot {
	context := s.session.BuildContext()
	tools := make([]protocol.ToolDefinition, 0, len(s.resources.Tools))
	for _, item := range s.resources.Tools {
		if item == nil {
			continue
		}
		tools = append(tools, item.Definition())
	}
	return SessionSnapshot{
		SessionID:  s.session.ID(),
		LeafID:     context.LeafID,
		EditorText: editorText,
		Messages:   protocol.CloneMessageList(context.Messages),
		Events:     cloneAgentEvents(s.events),
		Tools:      protocol.CloneTools(tools),
		Entries:    s.session.Entries(),
	}
}

func (s *AgentSession) Prompt(ctx context.Context, messages protocol.MessageList) (protocol.MessageList, error) {
	s.mu.Lock()
	agent := s.agent
	s.mu.Unlock()
	return agent.Prompt(ctx, messages)
}

func (s *AgentSession) NavigateTreeSnapshot(leafID string) (SessionSnapshot, error) {
	return s.navigateTree(leafID, "")
}

func (s *AgentSession) NavigateTreeWithSummarySnapshot(leafID, summary string) (SessionSnapshot, error) {
	return s.navigateTree(leafID, summary)
}

func (s *AgentSession) navigateTree(leafID, summary string) (SessionSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.agent.State().IsStreaming {
		return SessionSnapshot{}, agentcore.ErrAgentBusy
	}
	target, err := s.session.ResolveNavigationTarget(leafID)
	if err != nil {
		return SessionSnapshot{}, err
	}
	if summary != "" {
		details, err := s.session.BranchSummaryDetails(target.LeafID)
		if err != nil {
			return SessionSnapshot{}, err
		}
		if s.repo != nil {
			if _, err := s.repo.AppendBranchSummary(s.session.ID(), target.LeafID, summary, details); err != nil {
				return SessionSnapshot{}, err
			}
		} else {
			if err := s.session.MoveTo(target.LeafID); err != nil {
				return SessionSnapshot{}, err
			}
			if _, err := s.session.AppendBranchSummaryWithDetails(summary, details); err != nil {
				return SessionSnapshot{}, err
			}
		}
		if err := s.rebuildAgentLocked(); err != nil {
			return SessionSnapshot{}, err
		}
		return s.snapshotLocked(target.EditorText), nil
	}
	if s.repo != nil {
		if _, err := s.repo.AppendEntry(s.session.ID(), Entry{Type: EntryLeaf, LeafID: target.LeafID}); err != nil {
			return SessionSnapshot{}, err
		}
	} else {
		if _, err := s.session.AppendLeaf(target.LeafID); err != nil {
			return SessionSnapshot{}, err
		}
	}
	if err := s.rebuildAgentLocked(); err != nil {
		return SessionSnapshot{}, err
	}
	return s.snapshotLocked(target.EditorText), nil
}

func (s *AgentSession) handleEvent(ctx context.Context, event protocol.AgentEvent, _ agentcore.AgentState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	switch event.Type {
	case protocol.AgentEventMessageEnd:
		if event.Message != nil {
			if s.repo != nil {
				if _, err := s.repo.AppendEntry(s.session.ID(), Entry{Type: EntryMessage, Message: protocol.CloneMessage(event.Message)}); err != nil {
					return err
				}
			} else {
				if _, err := s.session.AppendMessage(event.Message); err != nil {
					return err
				}
			}
		}
	case protocol.AgentEventAgentEnd:
		if err := s.rebuildAgentLocked(); err != nil {
			return err
		}
	}
	return nil
}

func (s *AgentSession) indexToolsLocked() error {
	s.allTools = map[string]agentcore.Tool{}
	for _, tool := range s.resources.Tools {
		if tool == nil {
			continue
		}
		def := tool.Definition()
		if def.Name == "" {
			return errors.New("tool definition requires name")
		}
		if _, exists := s.allTools[def.Name]; exists {
			return fmt.Errorf("duplicate tool %q", def.Name)
		}
		s.allTools[def.Name] = tool
	}
	if len(s.resources.ToolNames) > 0 {
		names, err := s.resolveToolNamesLocked(s.resources.ToolNames)
		if err != nil {
			return err
		}
		s.resources.ToolNames = names
	}
	if len(s.activeToolNames) > 0 {
		names, err := s.resolveToolNamesLocked(s.activeToolNames)
		if err != nil {
			return err
		}
		s.activeToolNames = names
	}
	return nil
}

func (s *AgentSession) rebuildAgentLocked() error {
	ctx, config, err := s.buildCoreInputsLocked()
	if err != nil {
		return err
	}
	s.agent = agentcore.NewAgent(agentcore.AgentOptions{Context: ctx, Config: config})
	s.agent.Listen(s.handleEvent)
	return nil
}

func (s *AgentSession) buildCoreInputsLocked() (agentcore.AgentContext, agentcore.AgentLoopConfig, error) {
	sessionCtx := s.session.BuildContext()
	names, err := s.activeNamesForContextLocked(sessionCtx)
	if err != nil {
		return agentcore.AgentContext{}, agentcore.AgentLoopConfig{}, err
	}
	tools := s.toolsByNameLocked(names)
	registry, err := agentcore.NewRegistry(tools)
	if err != nil {
		return agentcore.AgentContext{}, agentcore.AgentLoopConfig{}, err
	}
	runner, err := agentcore.NewRunner(agentcore.RunnerConfig{Registry: registry})
	if err != nil {
		return agentcore.AgentContext{}, agentcore.AgentLoopConfig{}, err
	}
	model := firstNonEmpty(sessionCtx.Model, s.model)
	provider := firstNonEmpty(sessionCtx.Provider, s.provider)
	reasoning := firstNonEmpty(sessionCtx.Reasoning, s.reasoning)
	config := s.config
	userPrepare := config.PrepareNextTurn
	config.Model = model
	config.Provider = provider
	config.Reasoning = reasoning
	config.ToolRunner = runner
	config.PrepareNextTurn = s.prepareNextTurn(userPrepare)
	return agentcore.AgentContext{
		SystemPrompt: composeSystemPrompt(s.resources.SystemPrompt),
		Messages:     sessionCtx.Messages,
		Tools:        registry.Definitions(),
	}, config, nil
}

func (s *AgentSession) prepareNextTurn(userPrepare func(context.Context, agentcore.TurnContext) (agentcore.TurnUpdate, error)) func(context.Context, agentcore.TurnContext) (agentcore.TurnUpdate, error) {
	return func(ctx context.Context, turn agentcore.TurnContext) (agentcore.TurnUpdate, error) {
		s.mu.Lock()
		contextSnapshot, config, err := s.buildCoreInputsLocked()
		s.mu.Unlock()
		if err != nil {
			return agentcore.TurnUpdate{}, err
		}
		update := agentcore.TurnUpdate{
			Context:    &contextSnapshot,
			Model:      config.Model,
			Provider:   config.Provider,
			Reasoning:  config.Reasoning,
			ToolRunner: config.ToolRunner,
		}
		if userPrepare == nil {
			return update, nil
		}
		userUpdate, err := userPrepare(ctx, turn)
		if err != nil {
			return agentcore.TurnUpdate{}, err
		}
		if userUpdate.Context != nil {
			update.Context = userUpdate.Context
		}
		if userUpdate.Model != "" {
			update.Model = userUpdate.Model
		}
		if userUpdate.Provider != "" {
			update.Provider = userUpdate.Provider
		}
		if userUpdate.Reasoning != "" {
			update.Reasoning = userUpdate.Reasoning
		}
		if userUpdate.ToolRunner != nil {
			update.ToolRunner = userUpdate.ToolRunner
		}
		return update, nil
	}
}

func (s *AgentSession) activeNamesForContextLocked(ctx Context) ([]string, error) {
	if len(ctx.ToolNames) > 0 {
		return s.resolveToolNamesLocked(ctx.ToolNames)
	}
	if len(s.activeToolNames) > 0 {
		return s.resolveToolNamesLocked(s.activeToolNames)
	}
	if len(s.resources.ToolNames) > 0 {
		return s.resolveToolNamesLocked(s.resources.ToolNames)
	}
	names := make([]string, 0, len(s.resources.Tools))
	for _, tool := range s.resources.Tools {
		if tool == nil {
			continue
		}
		names = append(names, tool.Definition().Name)
	}
	return s.resolveToolNamesLocked(names)
}

func (s *AgentSession) resolveToolNamesLocked(names []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(names))
	for _, name := range names {
		if name == "" {
			return nil, errors.New("active tool name is required")
		}
		if seen[name] {
			return nil, fmt.Errorf("duplicate active tool %q", name)
		}
		if _, ok := s.allTools[name]; !ok {
			return nil, fmt.Errorf("unknown active tool %q", name)
		}
		seen[name] = true
		out = append(out, name)
	}
	return out, nil
}

func (s *AgentSession) toolsByNameLocked(names []string) []agentcore.Tool {
	tools := make([]agentcore.Tool, 0, len(names))
	for _, name := range names {
		if tool := s.allTools[name]; tool != nil {
			tools = append(tools, tool)
		}
	}
	return tools
}

func cloneAgentEvents(events []protocol.AgentEvent) []protocol.AgentEvent {
	if len(events) == 0 {
		return nil
	}
	out := make([]protocol.AgentEvent, len(events))
	for i, event := range events {
		event.Message = protocol.CloneMessage(event.Message)
		event.Messages = protocol.CloneMessageList(event.Messages)
		event.ToolResults = protocol.CloneToolResultMessages(event.ToolResults)
		if event.AssistantMessageEvent != nil {
			assistantEvent := *event.AssistantMessageEvent
			if assistantEvent.ToolCall != nil {
				toolCall := protocol.CloneToolCallContent(*assistantEvent.ToolCall)
				assistantEvent.ToolCall = &toolCall
			}
			if assistantEvent.Partial != nil {
				assistantEvent.Partial = protocol.CloneAssistantMessagePtr(assistantEvent.Partial)
			}
			if assistantEvent.Message != nil {
				assistantEvent.Message = protocol.CloneAssistantMessagePtr(assistantEvent.Message)
			}
			if assistantEvent.Error != nil {
				assistantEvent.Error = protocol.CloneAssistantMessagePtr(assistantEvent.Error)
			}
			event.AssistantMessageEvent = &assistantEvent
		}
		event.Args = cloneEventRaw(event.Args)
		if event.Result != nil {
			result := protocol.CloneToolResult(*event.Result)
			event.Result = &result
		}
		out[i] = event
	}
	return out
}

func cloneEventRaw(data []byte) []byte {
	if len(data) == 0 {
		return nil
	}
	return bytes.Clone(data)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
