package harness

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"oops/internal/llm/ai/protocol"
	coreagent "oops/internal/llm/core/agent"
	"oops/internal/llm/core/toolruntime"
	"oops/internal/llm/runtime/session"
)

type AgentSessionOptions struct {
	Session         *session.Session
	Repo            *session.Repository
	Resources       ResourceSnapshot
	Config          coreagent.AgentLoopConfig
	Model           string
	Provider        string
	Reasoning       string
	ActiveToolNames []string
}

type AgentSession struct {
	mu sync.Mutex

	session *session.Session
	repo    *session.Repository

	resources       ResourceSnapshot
	allTools        map[string]toolruntime.Tool
	activeToolNames []string

	config    coreagent.AgentLoopConfig
	model     string
	provider  string
	reasoning string

	agent *coreagent.Agent

	pending []pendingWrite
	events  []protocol.AgentEvent
	settled bool
}

type SessionSnapshot struct {
	SessionID string                    `json:"sessionId"`
	LeafID    string                    `json:"leafId,omitempty"`
	Messages  protocol.MessageList      `json:"messages"`
	Events    []protocol.AgentEvent     `json:"events"`
	Tools     []protocol.ToolDefinition `json:"tools"`
	Entries   []session.Entry           `json:"entries"`
}

type pendingWrite struct {
	kind      session.EntryType
	provider  string
	model     string
	reasoning string
	tools     []string
	message   protocol.AgentMessage
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
		settled:         true,
	}
	if err := as.indexToolsLocked(); err != nil {
		return nil, err
	}
	if err := as.rebuildAgentLocked(); err != nil {
		return nil, err
	}
	return as, nil
}

func (s *AgentSession) State() coreagent.AgentState {
	s.mu.Lock()
	agent := s.agent
	s.mu.Unlock()
	return agent.State()
}

func (s *AgentSession) SessionContext() session.Context {
	return s.session.BuildContext()
}

func (s *AgentSession) Events() []protocol.AgentEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.events) == 0 {
		return nil
	}
	return cloneAgentEvents(s.events)
}

func (s *AgentSession) Listen(listener coreagent.AgentListener) coreagent.Unsubscribe {
	s.mu.Lock()
	agent := s.agent
	s.mu.Unlock()
	return agent.Listen(listener)
}

func (s *AgentSession) Snapshot() SessionSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	context := s.session.BuildContext()
	tools := make([]protocol.ToolDefinition, 0, len(s.resources.Tools))
	for _, item := range s.resources.Tools {
		if item == nil {
			continue
		}
		tools = append(tools, item.Definition())
	}
	return SessionSnapshot{
		SessionID: s.session.ID(),
		LeafID:    context.LeafID,
		Messages:  protocol.CloneMessageList(context.Messages),
		Events:    cloneAgentEvents(s.events),
		Tools:     protocol.CloneTools(tools),
		Entries:   s.session.Entries(),
	}
}

func (s *AgentSession) Prompt(ctx context.Context, messages protocol.MessageList) (protocol.MessageList, error) {
	s.mu.Lock()
	s.settled = false
	agent := s.agent
	s.mu.Unlock()
	return agent.Prompt(ctx, messages)
}

func (s *AgentSession) Continue(ctx context.Context) (protocol.MessageList, error) {
	s.mu.Lock()
	s.settled = false
	agent := s.agent
	s.mu.Unlock()
	return agent.Continue(ctx)
}

func (s *AgentSession) Steer(messages protocol.MessageList) error {
	s.mu.Lock()
	agent := s.agent
	s.mu.Unlock()
	return agent.Steer(messages)
}

func (s *AgentSession) FollowUp(messages protocol.MessageList) error {
	s.mu.Lock()
	agent := s.agent
	s.mu.Unlock()
	return agent.FollowUp(messages)
}

func (s *AgentSession) Abort() bool {
	s.mu.Lock()
	agent := s.agent
	s.mu.Unlock()
	return agent.Abort()
}

func (s *AgentSession) WaitForIdle(ctx context.Context) error {
	s.mu.Lock()
	agent := s.agent
	s.mu.Unlock()
	return agent.WaitForIdle(ctx)
}

func (s *AgentSession) SetModel(provider, model string) error {
	if model == "" {
		return errors.New("model is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.provider = provider
	s.model = model
	if s.agent.State().IsStreaming {
		s.pending = append(s.pending, pendingWrite{kind: session.EntryModelChange, provider: provider, model: model})
		return nil
	}
	entry, err := s.session.AppendModelChange(provider, model)
	if err != nil {
		return err
	}
	if err := s.saveEntryLocked(entry); err != nil {
		return err
	}
	return s.rebuildAgentLocked()
}

func (s *AgentSession) SetReasoning(reasoning string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reasoning = reasoning
	if s.agent.State().IsStreaming {
		s.pending = append(s.pending, pendingWrite{kind: session.EntryThinkingLevelChange, reasoning: reasoning})
		return nil
	}
	entry, err := s.session.AppendThinkingLevelChange(reasoning)
	if err != nil {
		return err
	}
	if err := s.saveEntryLocked(entry); err != nil {
		return err
	}
	return s.rebuildAgentLocked()
}

func (s *AgentSession) SetActiveTools(names []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	names, err := s.resolveToolNamesLocked(names)
	if err != nil {
		return err
	}
	s.activeToolNames = cloneStrings(names)
	if s.agent.State().IsStreaming {
		s.pending = append(s.pending, pendingWrite{kind: session.EntryActiveToolsChange, tools: cloneStrings(names)})
		return nil
	}
	entry, err := s.session.AppendActiveToolsChange(names)
	if err != nil {
		return err
	}
	if err := s.saveEntryLocked(entry); err != nil {
		return err
	}
	return s.rebuildAgentLocked()
}

func (s *AgentSession) AppendCustomMessage(message protocol.AgentMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.agent.State().IsStreaming {
		s.pending = append(s.pending, pendingWrite{kind: session.EntryCustomMessage, message: protocol.CloneMessage(message)})
		return nil
	}
	entry, err := s.session.AppendCustomMessageEntry(message)
	if err != nil {
		return err
	}
	if err := s.saveEntryLocked(entry); err != nil {
		return err
	}
	return s.rebuildAgentLocked()
}

func (s *AgentSession) Compact(summary string, firstKeptEntryID string, tokensBefore int) (session.Entry, error) {
	if err := validateCompaction(summary, firstKeptEntryID); err != nil {
		return session.Entry{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.agent.State().IsStreaming {
		return session.Entry{}, coreagent.ErrAgentBusy
	}
	protectedFirstKeptID, err := s.session.ProtectedFirstKeptEntryID(firstKeptEntryID)
	if err != nil {
		return session.Entry{}, err
	}
	details, err := s.session.SummaryDetailsBefore(protectedFirstKeptID)
	if err != nil {
		return session.Entry{}, err
	}
	entry, err := s.session.AppendCompactionWithDetails(summary, protectedFirstKeptID, tokensBefore, details)
	if err != nil {
		return session.Entry{}, err
	}
	if err := s.saveEntryLocked(entry); err != nil {
		return session.Entry{}, err
	}
	if err := s.rebuildAgentLocked(); err != nil {
		return session.Entry{}, err
	}
	return entry, nil
}

func (s *AgentSession) NavigateTree(leafID string) error {
	return s.navigateTree(leafID, "")
}

func (s *AgentSession) NavigateTreeWithSummary(leafID, summary string) error {
	return s.navigateTree(leafID, summary)
}

func (s *AgentSession) navigateTree(leafID, summary string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.agent.State().IsStreaming {
		return coreagent.ErrAgentBusy
	}
	if summary != "" {
		details, err := s.session.BranchSummaryDetails(leafID)
		if err != nil {
			return err
		}
		if err := s.session.MoveTo(leafID); err != nil {
			return err
		}
		entry, err := s.session.AppendBranchSummaryWithDetails(summary, details)
		if err != nil {
			return err
		}
		if err := s.saveEntryLocked(entry); err != nil {
			return err
		}
		return s.rebuildAgentLocked()
	}
	entry, err := s.session.AppendLeaf(leafID)
	if err != nil {
		return err
	}
	if err := s.saveEntryLocked(entry); err != nil {
		return err
	}
	return s.rebuildAgentLocked()
}

func (s *AgentSession) handleEvent(ctx context.Context, event protocol.AgentEvent, _ coreagent.AgentState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	switch event.Type {
	case protocol.AgentEventMessageEnd:
		if event.Message != nil {
			entry, err := s.session.AppendMessage(event.Message)
			if err != nil {
				return err
			}
			if err := s.saveEntryLocked(entry); err != nil {
				return err
			}
		}
	case protocol.AgentEventTurnEnd:
		if err := s.flushPendingLocked(); err != nil {
			return err
		}
	case protocol.AgentEventAgentEnd:
		s.settled = true
		if err := s.rebuildAgentLocked(); err != nil {
			return err
		}
	}
	return nil
}

func (s *AgentSession) flushPendingLocked() error {
	pending := s.pending
	s.pending = nil
	for _, item := range pending {
		var (
			entry session.Entry
			err   error
		)
		switch item.kind {
		case session.EntryModelChange:
			entry, err = s.session.AppendModelChange(item.provider, item.model)
		case session.EntryThinkingLevelChange:
			entry, err = s.session.AppendThinkingLevelChange(item.reasoning)
		case session.EntryActiveToolsChange:
			entry, err = s.session.AppendActiveToolsChange(item.tools)
		case session.EntryCustomMessage:
			entry, err = s.session.AppendCustomMessageEntry(item.message)
		default:
			err = fmt.Errorf("unsupported pending write %q", item.kind)
		}
		if err != nil {
			return err
		}
		if err := s.saveEntryLocked(entry); err != nil {
			return err
		}
	}
	return nil
}

func (s *AgentSession) saveEntryLocked(entry session.Entry) error {
	if s.repo == nil {
		return nil
	}
	return s.repo.SaveEntry(s.session.ID(), entry)
}

func (s *AgentSession) indexToolsLocked() error {
	s.allTools = map[string]toolruntime.Tool{}
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
	s.agent = coreagent.NewAgent(coreagent.AgentOptions{Context: ctx, Config: config})
	s.agent.Listen(s.handleEvent)
	return nil
}

func (s *AgentSession) buildCoreInputsLocked() (coreagent.AgentContext, coreagent.AgentLoopConfig, error) {
	sessionCtx := s.session.BuildContext()
	names, err := s.activeNamesForContextLocked(sessionCtx)
	if err != nil {
		return coreagent.AgentContext{}, coreagent.AgentLoopConfig{}, err
	}
	tools := s.toolsByNameLocked(names)
	registry, err := toolruntime.NewRegistry(tools)
	if err != nil {
		return coreagent.AgentContext{}, coreagent.AgentLoopConfig{}, err
	}
	runner, err := toolruntime.NewRunner(toolruntime.RunnerConfig{Registry: registry})
	if err != nil {
		return coreagent.AgentContext{}, coreagent.AgentLoopConfig{}, err
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
	return coreagent.AgentContext{
		SystemPrompt: composeSystemPrompt(s.resources.SystemPrompt),
		Messages:     sessionCtx.Messages,
		Tools:        registry.Definitions(),
	}, config, nil
}

func (s *AgentSession) prepareNextTurn(userPrepare func(context.Context, coreagent.TurnContext) (coreagent.TurnUpdate, error)) func(context.Context, coreagent.TurnContext) (coreagent.TurnUpdate, error) {
	return func(ctx context.Context, turn coreagent.TurnContext) (coreagent.TurnUpdate, error) {
		s.mu.Lock()
		contextSnapshot, config, err := s.buildCoreInputsLocked()
		s.mu.Unlock()
		if err != nil {
			return coreagent.TurnUpdate{}, err
		}
		update := coreagent.TurnUpdate{
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
			return coreagent.TurnUpdate{}, err
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

func (s *AgentSession) activeNamesForContextLocked(ctx session.Context) ([]string, error) {
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

func (s *AgentSession) toolsByNameLocked(names []string) []toolruntime.Tool {
	tools := make([]toolruntime.Tool, 0, len(names))
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
		event.Args = cloneRaw(event.Args)
		if event.Result != nil {
			result := protocol.CloneToolResult(*event.Result)
			event.Result = &result
		}
		out[i] = event
	}
	return out
}

func cloneRaw(data []byte) []byte {
	if len(data) == 0 {
		return nil
	}
	out := make([]byte, len(data))
	copy(out, data)
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
