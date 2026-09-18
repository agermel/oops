package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	protocol "oops/internal/agent/ai"
	agentcore "oops/internal/agent/core"
	"oops/internal/agent/runtime/model"
	agentresources "oops/internal/agent/runtime/resources"
	"oops/internal/agent/runtime/session"
	agentskills "oops/internal/agent/runtime/skills"
)

type AgentHarnessOptions struct {
	Session          *session.Session
	Repo             *session.Repository
	Resources        agentresources.Snapshot
	Config           agentcore.AgentLoopConfig
	ProviderClient   *model.Client
	RequestOptions   *model.RequestOptions
	Model            string
	Provider         string
	Reasoning        string
	ActiveToolNames  []string
	PromptTemplates  []PromptTemplate
	BeforeAgentStart BeforeAgentStartHook
	SteeringMode     agentcore.QueueMode
	FollowUpMode     agentcore.QueueMode
}

type AbortResult struct {
	ClearedSteering protocol.MessageList
	ClearedFollowUp protocol.MessageList
}

type AgentHarness struct {
	mu    sync.Mutex
	runMu sync.Mutex

	session *session.Session
	repo    *session.Repository
	phase   AgentHarnessPhase

	resources        agentresources.Snapshot
	allTools         map[string]agentcore.Tool
	activeToolNames  []string
	activeToolsSet   bool
	nextTurnQueue    protocol.MessageList
	promptTemplates  []PromptTemplate
	beforeAgentStart BeforeAgentStartHook
	steeringMode     agentcore.QueueMode
	followUpMode     agentcore.QueueMode

	config    agentcore.AgentLoopConfig
	model     string
	provider  string
	reasoning string

	providerClient         *model.Client
	providerRequestOptions model.RequestOptions
	providerTurnOptions    model.RequestOptions
	providerTurnOptionsSet bool

	agent       *agentcore.Agent
	activeAgent *agentcore.Agent
	runCancel   context.CancelFunc
	runDone     chan struct{}
	acceptQueue bool

	nextListenerID int
	listenerOrder  []int
	listeners      map[int]agentcore.AgentListener

	nextRunListenerID int
	runListenerOrder  []int
	runListeners      map[int]func(context.Context, RunEvent) error

	pendingSessionWrites []session.Entry

	events []protocol.AgentEvent
}

type SessionSnapshot struct {
	SessionID  string                    `json:"sessionId"`
	LeafID     string                    `json:"leafId,omitempty"`
	EditorText string                    `json:"editorText,omitempty"`
	Messages   protocol.MessageList      `json:"messages"`
	Events     []protocol.AgentEvent     `json:"events"`
	Tools      []protocol.ToolDefinition `json:"tools"`
	Entries    []session.Entry           `json:"entries"`
}

// MarshalJSON keeps snapshot collections as arrays at both HTTP and SSE boundaries.
func (snapshot SessionSnapshot) MarshalJSON() ([]byte, error) {
	type plainSnapshot SessionSnapshot
	if snapshot.Messages == nil {
		snapshot.Messages = protocol.MessageList{}
	}
	if snapshot.Events == nil {
		snapshot.Events = []protocol.AgentEvent{}
	}
	if snapshot.Tools == nil {
		snapshot.Tools = []protocol.ToolDefinition{}
	}
	if snapshot.Entries == nil {
		snapshot.Entries = []session.Entry{}
	}
	return json.Marshal(plainSnapshot(snapshot))
}

func ClearSnapshotPayload(snapshot SessionSnapshot) SessionSnapshot {
	snapshot.EditorText = ""
	snapshot.Messages = protocol.MessageList{}
	snapshot.Events = []protocol.AgentEvent{}
	snapshot.Tools = []protocol.ToolDefinition{}
	snapshot.Entries = []session.Entry{}
	return snapshot
}

func NewAgentHarness(options AgentHarnessOptions) (*AgentHarness, error) {
	return newAgentHarness(options, true)
}

func newAgentHarness(options AgentHarnessOptions, registerSession bool) (*AgentHarness, error) {
	if options.Session == nil {
		return nil, errors.New("agent harness requires session")
	}
	sessionContext := options.Session.BuildContext()
	providerClient, requestOptions, err := resolveProviderBinding(options.ProviderClient, options.RequestOptions)
	if err != nil {
		return nil, err
	}
	repo := options.Repo
	if repo == nil {
		repo = session.NewRepository(nil)
	}
	activeToolNames := slices.Clone(options.ActiveToolNames)
	activeToolsSet := options.ActiveToolNames != nil
	if sessionContext.ToolNamesSet {
		activeToolNames = slices.Clone(sessionContext.ToolNames)
		activeToolsSet = true
	}
	as := &AgentHarness{
		session:                options.Session,
		repo:                   repo,
		phase:                  AgentHarnessPhaseIdle,
		resources:              agentresources.Clone(options.Resources),
		config:                 options.Config,
		model:                  firstNonEmpty(sessionContext.Model, options.Model),
		provider:               firstNonEmpty(sessionContext.Provider, options.Provider),
		reasoning:              firstNonEmpty(sessionContext.Reasoning, options.Reasoning),
		activeToolNames:        activeToolNames,
		activeToolsSet:         activeToolsSet,
		promptTemplates:        clonePromptTemplates(options.PromptTemplates),
		beforeAgentStart:       options.BeforeAgentStart,
		steeringMode:           options.SteeringMode,
		followUpMode:           options.FollowUpMode,
		listeners:              map[int]agentcore.AgentListener{},
		runListeners:           map[int]func(context.Context, RunEvent) error{},
		providerClient:         providerClient,
		providerRequestOptions: requestOptions,
	}
	if as.providerClient != nil {
		as.config.Stream = as.providerStream
	}
	if err := as.indexToolsLocked(); err != nil {
		return nil, err
	}
	if err := as.rebuildAgentLocked(); err != nil {
		return nil, err
	}
	if registerSession {
		if err := as.repo.CreateSession(as.session); err != nil {
			return nil, err
		}
	}
	return as, nil
}

func (s *AgentHarness) refresh(options AgentHarnessOptions) error {
	sessionContext := s.session.BuildContext()
	providerClient, requestOptions, err := resolveProviderBinding(options.ProviderClient, options.RequestOptions)
	if err != nil {
		return err
	}
	resources := agentresources.Clone(options.Resources)
	activeToolNames := slices.Clone(options.ActiveToolNames)
	activeToolsSet := options.ActiveToolNames != nil
	if sessionContext.ToolNamesSet {
		activeToolNames = slices.Clone(sessionContext.ToolNames)
		activeToolsSet = true
	}
	promptTemplates := clonePromptTemplates(options.PromptTemplates)
	config := options.Config
	if providerClient != nil {
		config.Stream = s.providerStream
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.canStartRunLocked(); err != nil {
		return err
	}

	oldResources := s.resources
	oldAllTools := s.allTools
	oldActiveToolNames := s.activeToolNames
	oldActiveToolsSet := s.activeToolsSet
	oldPromptTemplates := s.promptTemplates
	oldBeforeAgentStart := s.beforeAgentStart
	oldConfig := s.config
	oldModel := s.model
	oldProvider := s.provider
	oldReasoning := s.reasoning
	oldProviderClient := s.providerClient
	oldProviderRequestOptions := s.providerRequestOptions
	oldProviderTurnOptions := s.providerTurnOptions
	oldProviderTurnOptionsSet := s.providerTurnOptionsSet
	oldAgent := s.agent

	restore := func() {
		s.resources = oldResources
		s.allTools = oldAllTools
		s.activeToolNames = oldActiveToolNames
		s.activeToolsSet = oldActiveToolsSet
		s.promptTemplates = oldPromptTemplates
		s.beforeAgentStart = oldBeforeAgentStart
		s.config = oldConfig
		s.model = oldModel
		s.provider = oldProvider
		s.reasoning = oldReasoning
		s.providerClient = oldProviderClient
		s.providerRequestOptions = oldProviderRequestOptions
		s.providerTurnOptions = oldProviderTurnOptions
		s.providerTurnOptionsSet = oldProviderTurnOptionsSet
		s.agent = oldAgent
	}

	s.resources = resources
	s.activeToolNames = activeToolNames
	s.activeToolsSet = activeToolsSet
	s.promptTemplates = promptTemplates
	s.beforeAgentStart = options.BeforeAgentStart
	s.config = config
	s.model = firstNonEmpty(sessionContext.Model, options.Model)
	s.provider = firstNonEmpty(sessionContext.Provider, options.Provider)
	s.reasoning = firstNonEmpty(sessionContext.Reasoning, options.Reasoning)
	s.providerClient = providerClient
	s.providerRequestOptions = requestOptions
	s.providerTurnOptions = model.RequestOptions{}
	s.providerTurnOptionsSet = false
	if err := s.indexToolsLocked(); err != nil {
		restore()
		return err
	}
	if err := s.rebuildAgentLocked(); err != nil {
		restore()
		return err
	}
	return nil
}

func (s *AgentHarness) Phase() AgentHarnessPhase {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.phase
}

func (s *AgentHarness) ValidateProviderContext() error {
	return s.session.ValidateContext()
}

func (s *AgentHarness) Listen(listener agentcore.AgentListener) agentcore.Unsubscribe {
	if listener == nil {
		return func() {}
	}
	s.mu.Lock()
	id := s.nextListenerID
	s.nextListenerID++
	s.listeners[id] = listener
	s.listenerOrder = append(s.listenerOrder, id)
	s.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.listeners, id)
			for i, value := range s.listenerOrder {
				if value == id {
					s.listenerOrder = append(s.listenerOrder[:i], s.listenerOrder[i+1:]...)
					break
				}
			}
			s.mu.Unlock()
		})
	}
}

func (s *AgentHarness) Snapshot() SessionSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked("")
}

func (s *AgentHarness) snapshotLocked(editorText string) SessionSnapshot {
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

func (s *AgentHarness) Prompt(ctx context.Context, messages protocol.MessageList) (result protocol.MessageList, err error) {
	s.mu.Lock()
	return s.promptLocked(ctx, messages)
}

func (s *AgentHarness) promptLocked(ctx context.Context, messages protocol.MessageList) (result protocol.MessageList, err error) {
	if err := s.canStartRunLocked(); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	inputMessages := protocol.CloneMessageList(messages)
	queuedMessages := protocol.CloneMessageList(s.nextTurnQueue)
	prompts := append(protocol.CloneMessageList(queuedMessages), inputMessages...)
	if err := validateMessages(prompts); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.phase = AgentHarnessPhaseTurn
	s.runMu.Lock()
	s.activeAgent = nil
	s.runCancel = cancel
	s.runDone = done
	s.acceptQueue = false
	s.runMu.Unlock()
	failStartLocked := func(startErr error, restoreQueue bool) (protocol.MessageList, error) {
		if restoreQueue {
			restored := protocol.CloneMessageList(queuedMessages)
			restored = append(restored, s.nextTurnQueue...)
			s.nextTurnQueue = restored
		}
		s.phase = AgentHarnessPhaseIdle
		nextTurnCount := len(s.nextTurnQueue)
		s.mu.Unlock()
		s.detachRun(done)
		return nil, errors.Join(startErr, s.settleRun(runCtx, cancel, done, nextTurnCount))
	}

	hadPendingWrites := len(s.pendingSessionWrites) > 0
	preflightErr := s.flushPendingSessionWritesLocked()
	if preflightErr == nil && hadPendingWrites {
		preflightErr = s.rebuildAgentLocked()
	}
	preflightErr = errors.Join(preflightErr, runCtx.Err())
	if preflightErr != nil {
		return failStartLocked(preflightErr, false)
	}
	agent := s.agent
	beforeAgentStart := s.beforeAgentStart
	var hookContext agentcore.AgentContext
	var hookConfig agentcore.AgentLoopConfig
	var hookInput BeforeAgentStartContext
	steeringMode := s.steeringMode
	followUpMode := s.followUpMode
	if beforeAgentStart != nil {
		var buildErr error
		hookContext, hookConfig, buildErr = s.buildCoreInputsLocked()
		if buildErr != nil {
			return failStartLocked(buildErr, false)
		}
		hookInput = BeforeAgentStartContext{
			Messages:     protocol.CloneMessageList(inputMessages),
			SystemPrompt: hookContext.SystemPrompt,
			Resources:    s.resourcesLocked(),
		}
	}
	s.nextTurnQueue = nil
	s.mu.Unlock()

	if beforeAgentStart != nil {
		hookResult, hookErr := beforeAgentStart(runCtx, hookInput)
		hookMessages := protocol.CloneMessageList(hookResult.Messages)
		if hookErr == nil {
			hookErr = validateMessages(hookMessages)
		}
		hookErr = errors.Join(hookErr, runCtx.Err())
		if hookErr != nil {
			s.mu.Lock()
			return failStartLocked(hookErr, true)
		}
		prompts = append(prompts, hookMessages...)
		if hookResult.SystemPrompt != nil {
			hookContext.SystemPrompt = *hookResult.SystemPrompt
			agent = agentcore.NewAgent(agentcore.AgentOptions{
				Context:      hookContext,
				Config:       hookConfig,
				SteeringMode: steeringMode,
				FollowUpMode: followUpMode,
			})
			agent.Listen(s.handleEvent)
		}
	}

	s.mu.Lock()
	s.runMu.Lock()
	startErr := runCtx.Err()
	if startErr == nil {
		s.activeAgent = agent
		s.acceptQueue = true
	}
	s.runMu.Unlock()
	if startErr != nil {
		return failStartLocked(startErr, true)
	}
	s.mu.Unlock()

	defer func() {
		err = errors.Join(err, s.finishRun(runCtx, agent, cancel, done))
	}()
	return agent.Prompt(runCtx, prompts)
}

func (s *AgentHarness) canStartRunLocked() error {
	s.runMu.Lock()
	runActive := s.runDone != nil
	s.runMu.Unlock()
	if s.phase != AgentHarnessPhaseIdle || runActive {
		return agentcore.ErrAgentBusy
	}
	return nil
}

func (s *AgentHarness) PromptFromTemplate(ctx context.Context, name string, args []string) (protocol.MessageList, error) {
	s.mu.Lock()
	if err := s.canStartRunLocked(); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	var selected *PromptTemplate
	for _, template := range s.promptTemplates {
		if template.Name == name {
			copy := template
			selected = &copy
			break
		}
	}
	if selected == nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("unknown prompt template %q", name)
	}
	message := protocol.UserMessage{
		Content:   protocol.ContentList{protocol.NewTextContent(FormatPromptTemplateInvocation(*selected, args))},
		Timestamp: time.Now().UnixMilli(),
	}
	return s.promptLocked(ctx, protocol.MessageList{message})
}

func (s *AgentHarness) Skill(ctx context.Context, name, additionalInstructions string) (protocol.MessageList, error) {
	s.mu.Lock()
	if err := s.canStartRunLocked(); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	var selected *agentskills.Skill
	for _, skill := range s.resources.Skills {
		if skill.Name == name && skill.Enabled {
			copy := skill
			selected = &copy
			break
		}
	}
	if selected == nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("unknown skill %q", name)
	}
	message := protocol.UserMessage{
		Content:   protocol.ContentList{protocol.NewTextContent(agentskills.FormatInvocation(selected, additionalInstructions))},
		Timestamp: time.Now().UnixMilli(),
	}
	return s.promptLocked(ctx, protocol.MessageList{message})
}

func (s *AgentHarness) Abort(ctx context.Context) (AbortResult, error) {
	s.runMu.Lock()
	agent := s.activeAgent
	if s.runDone == nil {
		s.runMu.Unlock()
		return AbortResult{}, nil
	}
	done := s.runDone
	cancel := s.runCancel
	if agent == nil {
		s.runMu.Unlock()
		if cancel != nil {
			cancel()
		}
		select {
		case <-done:
			return AbortResult{}, nil
		case <-ctx.Done():
			return AbortResult{}, ctx.Err()
		}
	}
	result := AbortResult{
		ClearedSteering: agent.ClearSteeringQueue(),
		ClearedFollowUp: agent.ClearFollowUpQueue(),
	}
	s.acceptQueue = false
	s.runMu.Unlock()

	if cancel != nil {
		cancel()
	}
	agent.Abort()
	select {
	case <-done:
		return result, nil
	case <-ctx.Done():
		return result, ctx.Err()
	}
}

func (s *AgentHarness) WaitForIdle(ctx context.Context) error {
	s.runMu.Lock()
	done := s.runDone
	s.runMu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *AgentHarness) finishRun(ctx context.Context, agent *agentcore.Agent, cancel context.CancelFunc, done chan struct{}) error {
	s.runMu.Lock()
	ownsRun := s.activeAgent == agent && s.runDone == done
	if ownsRun {
		s.acceptQueue = false
	}
	s.runMu.Unlock()

	s.mu.Lock()
	var settleErr error
	if ownsRun {
		s.drainActiveQueuesLocked()
		flushErr := s.flushPendingSessionWritesLocked()
		if flushErr == nil {
			settleErr = s.rebuildAgentLocked()
		}
		settleErr = errors.Join(flushErr, settleErr)
		s.phase = AgentHarnessPhaseIdle
	}
	nextTurnCount := len(s.nextTurnQueue)
	s.mu.Unlock()

	s.detachRun(done)
	settledErr := s.settleRun(ctx, cancel, done, nextTurnCount)
	return errors.Join(settleErr, settledErr)
}

func (s *AgentHarness) detachRun(done chan struct{}) {
	s.runMu.Lock()
	if s.runDone == done {
		s.activeAgent = nil
		s.runCancel = nil
		s.acceptQueue = false
	}
	s.runMu.Unlock()
}

func (s *AgentHarness) settleRun(ctx context.Context, cancel context.CancelFunc, done chan struct{}, nextTurnCount int) (err error) {
	defer func() {
		cancel()
		s.runMu.Lock()
		if s.runDone == done {
			s.runDone = nil
			close(done)
		}
		s.runMu.Unlock()
	}()
	return s.emitRunEvent(ctx, SettledEvent{
		Type:          "settled",
		NextTurnCount: nextTurnCount,
	})
}

func (s *AgentHarness) Steer(messages protocol.MessageList) error {
	return s.enqueueWhileRunning(messages, "steer", (*agentcore.Agent).Steer)
}

func (s *AgentHarness) FollowUp(messages protocol.MessageList) error {
	return s.enqueueWhileRunning(messages, "follow-up", (*agentcore.Agent).FollowUp)
}

func (s *AgentHarness) NextTurn(messages protocol.MessageList) error {
	if err := validateQueuedMessages(messages); err != nil {
		return err
	}
	s.mu.Lock()
	s.nextTurnQueue = append(s.nextTurnQueue, protocol.CloneMessageList(messages)...)
	s.mu.Unlock()
	return nil
}

func (s *AgentHarness) AppendMessage(message protocol.AgentMessage) error {
	if message == nil {
		return errors.New("message is required")
	}
	if err := message.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	queued, err := s.persistOrQueueSessionMutationLocked(session.Entry{
		Type:    session.EntryMessage,
		Message: protocol.CloneMessage(message),
	})
	if err != nil || queued {
		return err
	}
	return s.rebuildAgentLocked()
}

func (s *AgentHarness) SetModel(provider, model string) error {
	if provider == "" {
		return errors.New("provider is required")
	}
	if model == "" {
		return errors.New("model is required")
	}
	s.mu.Lock()
	previousProvider := s.provider
	previousModel := s.model
	queued, err := s.persistOrQueueSessionMutationLocked(session.Entry{
		Type:     session.EntryModelChange,
		Provider: provider,
		Model:    model,
	})
	if err != nil && !queued {
		s.mu.Unlock()
		return err
	}
	s.provider = provider
	s.model = model
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if !queued {
		if err := s.rebuildAgentLocked(); err != nil {
			s.mu.Unlock()
			return err
		}
	}
	s.mu.Unlock()
	return s.emitRunEvent(context.Background(), ModelUpdateEvent{
		Type:             "model_update",
		Provider:         provider,
		Model:            model,
		PreviousProvider: previousProvider,
		PreviousModel:    previousModel,
		Source:           "set",
	})
}

func (s *AgentHarness) SetReasoning(reasoning string) error {
	if reasoning == "" {
		reasoning = "off"
	}
	s.mu.Lock()
	previousReasoning := s.reasoning
	queued, err := s.persistOrQueueSessionMutationLocked(session.Entry{
		Type:      session.EntryThinkingLevelChange,
		Reasoning: reasoning,
	})
	if err != nil && !queued {
		s.mu.Unlock()
		return err
	}
	s.reasoning = reasoning
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if !queued {
		if err := s.rebuildAgentLocked(); err != nil {
			s.mu.Unlock()
			return err
		}
	}
	s.mu.Unlock()
	return s.emitRunEvent(context.Background(), ThinkingLevelUpdateEvent{
		Type:          "thinking_level_update",
		Level:         reasoning,
		PreviousLevel: previousReasoning,
	})
}

func (s *AgentHarness) SetActiveTools(names []string) error {
	s.mu.Lock()
	resolved, err := s.resolveToolNamesLocked(names)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	previousToolNames := s.toolNamesLocked()
	previousActiveToolNames, err := s.effectiveActiveToolNamesLocked()
	if err != nil {
		s.mu.Unlock()
		return err
	}
	queued, err := s.persistOrQueueSessionMutationLocked(session.Entry{
		Type:      session.EntryActiveToolsChange,
		ToolNames: slices.Clone(resolved),
	})
	if err != nil && !queued {
		s.mu.Unlock()
		return err
	}
	s.activeToolNames = slices.Clone(resolved)
	s.activeToolsSet = true
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if !queued {
		if err := s.rebuildAgentLocked(); err != nil {
			s.mu.Unlock()
			return err
		}
	}
	toolNames := s.toolNamesLocked()
	activeToolNames := slices.Clone(s.activeToolNames)
	s.mu.Unlock()
	return s.emitRunEvent(context.Background(), ToolsUpdateEvent{
		Type:                    "tools_update",
		ToolNames:               toolNames,
		PreviousToolNames:       previousToolNames,
		ActiveToolNames:         activeToolNames,
		PreviousActiveToolNames: previousActiveToolNames,
		Source:                  "set",
	})
}

func (s *AgentHarness) SetTools(tools []agentcore.Tool, activeNames []string) error {
	nextTools := slices.Clone(tools)
	s.mu.Lock()
	previousToolNames := s.toolNamesLocked()
	previousActiveToolNames, err := s.effectiveActiveToolNamesLocked()
	if err != nil {
		s.mu.Unlock()
		return err
	}
	nextIndex, nextToolNames, err := indexToolList(nextTools)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	nextActiveToolNames := slices.Clone(activeNames)
	if activeNames == nil {
		nextActiveToolNames = slices.Clone(previousActiveToolNames)
	}
	nextActiveToolNames, err = resolveToolNames(nextActiveToolNames, nextIndex)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if _, err := agentcore.NewRegistry(toolsByName(nextIndex, nextActiveToolNames)); err != nil {
		s.mu.Unlock()
		return err
	}

	pendingCount := len(s.pendingSessionWrites)
	queued, err := s.persistOrQueueSessionMutationLocked(session.Entry{
		Type:      session.EntryActiveToolsChange,
		ToolNames: slices.Clone(nextActiveToolNames),
	})
	if err != nil {
		if queued && len(s.pendingSessionWrites) > pendingCount {
			s.pendingSessionWrites = s.pendingSessionWrites[:len(s.pendingSessionWrites)-1]
		}
		s.mu.Unlock()
		return err
	}

	previousResources := s.resources
	previousAllTools := s.allTools
	previousActiveNames := s.activeToolNames
	previousActiveSet := s.activeToolsSet
	previousAgent := s.agent
	s.resources.Tools = nextTools
	s.resources.ToolNames = nil
	s.allTools = nextIndex
	s.activeToolNames = slices.Clone(nextActiveToolNames)
	s.activeToolsSet = true
	if !queued {
		if err := s.rebuildAgentLocked(); err != nil {
			s.resources = previousResources
			s.allTools = previousAllTools
			s.activeToolNames = previousActiveNames
			s.activeToolsSet = previousActiveSet
			s.agent = previousAgent
			s.mu.Unlock()
			return err
		}
	}
	s.mu.Unlock()
	return s.emitRunEvent(context.Background(), ToolsUpdateEvent{
		Type:                    "tools_update",
		ToolNames:               slices.Clone(nextToolNames),
		PreviousToolNames:       slices.Clone(previousToolNames),
		ActiveToolNames:         slices.Clone(nextActiveToolNames),
		PreviousActiveToolNames: slices.Clone(previousActiveToolNames),
		Source:                  "set",
	})
}

func (s *AgentHarness) Resources() AgentHarnessResources {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resourcesLocked()
}

func (s *AgentHarness) SetResources(resources AgentHarnessResources) error {
	next := cloneHarnessResources(resources)
	s.mu.Lock()
	previous := s.resourcesLocked()
	s.resources.Skills = next.Skills
	s.promptTemplates = next.PromptTemplates
	current := s.resourcesLocked()
	s.mu.Unlock()
	return s.emitRunEvent(context.Background(), ResourcesUpdateEvent{
		Type:              "resources_update",
		Resources:         current,
		PreviousResources: previous,
	})
}

func (s *AgentHarness) persistOrQueueSessionMutationLocked(entry session.Entry) (bool, error) {
	if s.phase != AgentHarnessPhaseIdle || len(s.pendingSessionWrites) > 0 {
		s.pendingSessionWrites = append(s.pendingSessionWrites, entry)
		if s.phase != AgentHarnessPhaseIdle {
			return true, nil
		}
		err := s.flushPendingSessionWritesLocked()
		return err != nil, err
	}
	if _, err := s.repo.AppendEntry(s.session.ID(), entry); err != nil {
		return false, err
	}
	return false, nil
}

func (s *AgentHarness) flushPendingSessionWritesLocked() error {
	for len(s.pendingSessionWrites) > 0 {
		entry := s.pendingSessionWrites[0]
		if _, err := s.repo.AppendEntry(s.session.ID(), entry); err != nil {
			return err
		}
		s.pendingSessionWrites[0] = session.Entry{}
		s.pendingSessionWrites = s.pendingSessionWrites[1:]
	}
	return nil
}

func (s *AgentHarness) enqueueWhileRunning(messages protocol.MessageList, operation string, enqueue func(*agentcore.Agent, protocol.MessageList) error) error {
	if err := validateQueuedMessages(messages); err != nil {
		return err
	}
	s.runMu.Lock()
	agent := s.activeAgent
	if agent == nil || !s.acceptQueue {
		s.runMu.Unlock()
		return fmt.Errorf("cannot %s while agent is idle", operation)
	}
	err := enqueue(agent, messages)
	s.runMu.Unlock()
	return err
}

func (s *AgentHarness) handleEvent(ctx context.Context, event protocol.AgentEvent, state agentcore.AgentState) error {
	s.mu.Lock()
	s.events = append(s.events, event)
	var persistenceErr error
	switch event.Type {
	case protocol.AgentEventTurnStart:
		s.captureProviderTurnOptionsLocked()
	case protocol.AgentEventMessageEnd:
		if event.Message != nil {
			_, persistenceErr = s.repo.AppendEntry(s.session.ID(), session.Entry{Type: session.EntryMessage, Message: protocol.CloneMessage(event.Message)})
		}
	case protocol.AgentEventAgentEnd:
		s.drainActiveQueuesLocked()
		persistenceErr = s.flushPendingSessionWritesLocked()
		if persistenceErr == nil {
			s.phase = AgentHarnessPhaseIdle
		}
	}
	listeners := s.listenerSnapshotLocked()
	runListeners := s.runEventListenerSnapshotLocked()
	s.mu.Unlock()
	if persistenceErr != nil {
		return persistenceErr
	}

	listenerErr := dispatchAgentListeners(ctx, listeners, event, state)
	runEventErr := dispatchRunEventListeners(ctx, runListeners, CoreAgentEvent(cloneAgentEvent(event)))
	eventErr := errors.Join(listenerErr, runEventErr)
	if event.Type != protocol.AgentEventTurnEnd {
		return eventErr
	}

	s.mu.Lock()
	hadPendingMutations := len(s.pendingSessionWrites) > 0
	flushErr := s.flushPendingSessionWritesLocked()
	s.mu.Unlock()
	if flushErr != nil {
		return errors.Join(eventErr, flushErr)
	}
	if eventErr != nil {
		return eventErr
	}
	return s.emitRunEvent(ctx, SavePointEvent{
		Type:                "save_point",
		HadPendingMutations: hadPendingMutations,
	})
}

func (s *AgentHarness) drainActiveQueuesLocked() {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	s.acceptQueue = false
	if s.activeAgent == nil {
		return
	}
	s.nextTurnQueue = append(s.nextTurnQueue, s.activeAgent.ClearSteeringQueue()...)
	s.nextTurnQueue = append(s.nextTurnQueue, s.activeAgent.ClearFollowUpQueue()...)
}

func (s *AgentHarness) listenerSnapshotLocked() []agentcore.AgentListener {
	if len(s.listenerOrder) == 0 {
		return nil
	}
	listeners := make([]agentcore.AgentListener, 0, len(s.listenerOrder))
	for _, id := range s.listenerOrder {
		if listener := s.listeners[id]; listener != nil {
			listeners = append(listeners, listener)
		}
	}
	return listeners
}

func dispatchAgentListeners(ctx context.Context, listeners []agentcore.AgentListener, event protocol.AgentEvent, state agentcore.AgentState) error {
	var listenerErrors []error
	for _, listener := range listeners {
		if err := listener(ctx, event, state); err != nil {
			listenerErrors = append(listenerErrors, err)
		}
	}
	return errors.Join(listenerErrors...)
}

func (s *AgentHarness) runEventListenerSnapshotLocked() []func(context.Context, RunEvent) error {
	if len(s.runListenerOrder) == 0 {
		return nil
	}
	listeners := make([]func(context.Context, RunEvent) error, 0, len(s.runListenerOrder))
	for _, id := range s.runListenerOrder {
		if listener := s.runListeners[id]; listener != nil {
			listeners = append(listeners, listener)
		}
	}
	return listeners
}

func (s *AgentHarness) emitRunEvent(ctx context.Context, event RunEvent) error {
	s.mu.Lock()
	listeners := s.runEventListenerSnapshotLocked()
	s.mu.Unlock()
	return dispatchRunEventListeners(ctx, listeners, event)
}

func dispatchRunEventListeners(ctx context.Context, listeners []func(context.Context, RunEvent) error, event RunEvent) error {
	var listenerErrors []error
	for _, listener := range listeners {
		if err := listener(ctx, cloneRunEvent(event)); err != nil {
			listenerErrors = append(listenerErrors, err)
		}
	}
	return errors.Join(listenerErrors...)
}

func (s *AgentHarness) indexToolsLocked() error {
	allTools, _, err := indexToolList(s.resources.Tools)
	if err != nil {
		return err
	}
	s.allTools = allTools
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

func (s *AgentHarness) rebuildAgentLocked() error {
	ctx, config, err := s.buildCoreInputsLocked()
	if err != nil {
		return err
	}
	s.agent = agentcore.NewAgent(agentcore.AgentOptions{
		Context:      ctx,
		Config:       config,
		SteeringMode: s.steeringMode,
		FollowUpMode: s.followUpMode,
	})
	s.agent.Listen(s.handleEvent)
	return nil
}

func (s *AgentHarness) buildCoreInputsLocked() (agentcore.AgentContext, agentcore.AgentLoopConfig, error) {
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
	runner, err := agentcore.NewRunner(agentcore.RunnerConfig{
		Registry: registry,
		Hooks: agentcore.Hooks{
			BeforeToolCall: s.config.BeforeToolCall,
			AfterToolCall:  s.config.AfterToolCall,
		},
	})
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
	config.SessionID = s.session.ID()
	config.ToolRunner = runner
	config.PrepareNextTurn = s.prepareNextTurn(userPrepare)
	return agentcore.AgentContext{
		SystemPrompt: composeSystemPrompt(s.resources.SystemPrompt),
		Messages:     sessionCtx.Messages,
		Tools:        registry.Definitions(),
	}, config, nil
}

func (s *AgentHarness) prepareNextTurn(userPrepare func(context.Context, agentcore.TurnContext) (agentcore.TurnUpdate, error)) func(context.Context, agentcore.TurnContext) (agentcore.TurnUpdate, error) {
	return func(ctx context.Context, turn agentcore.TurnContext) (agentcore.TurnUpdate, error) {
		s.mu.Lock()
		if err := s.flushPendingSessionWritesLocked(); err != nil {
			s.mu.Unlock()
			return agentcore.TurnUpdate{}, err
		}
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

func (s *AgentHarness) activeNamesForContextLocked(ctx session.Context) ([]string, error) {
	if ctx.ToolNamesSet {
		return s.resolveToolNamesLocked(ctx.ToolNames)
	}
	if s.activeToolsSet {
		return s.resolveToolNamesLocked(s.activeToolNames)
	}
	if s.resources.ToolNames != nil {
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

func (s *AgentHarness) resolveToolNamesLocked(names []string) ([]string, error) {
	return resolveToolNames(names, s.allTools)
}

func resolveToolNames(names []string, tools map[string]agentcore.Tool) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(names))
	for _, name := range names {
		if name == "" {
			return nil, errors.New("active tool name is required")
		}
		if seen[name] {
			return nil, fmt.Errorf("duplicate active tool %q", name)
		}
		if _, ok := tools[name]; !ok {
			return nil, fmt.Errorf("unknown active tool %q", name)
		}
		seen[name] = true
		out = append(out, name)
	}
	return out, nil
}

func (s *AgentHarness) toolsByNameLocked(names []string) []agentcore.Tool {
	return toolsByName(s.allTools, names)
}

func toolsByName(allTools map[string]agentcore.Tool, names []string) []agentcore.Tool {
	tools := make([]agentcore.Tool, 0, len(names))
	for _, name := range names {
		if tool := allTools[name]; tool != nil {
			tools = append(tools, tool)
		}
	}
	return tools
}

func indexToolList(tools []agentcore.Tool) (map[string]agentcore.Tool, []string, error) {
	indexed := make(map[string]agentcore.Tool, len(tools))
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		name := tool.Definition().Name
		if name == "" {
			return nil, nil, errors.New("tool definition requires name")
		}
		if _, exists := indexed[name]; exists {
			return nil, nil, fmt.Errorf("duplicate tool %q", name)
		}
		indexed[name] = tool
		names = append(names, name)
	}
	return indexed, names, nil
}

func (s *AgentHarness) toolNamesLocked() []string {
	names := make([]string, 0, len(s.resources.Tools))
	for _, tool := range s.resources.Tools {
		if tool == nil {
			continue
		}
		names = append(names, tool.Definition().Name)
	}
	return names
}

func (s *AgentHarness) effectiveActiveToolNamesLocked() ([]string, error) {
	if s.activeToolsSet {
		return s.resolveToolNamesLocked(s.activeToolNames)
	}
	if s.resources.ToolNames != nil {
		return s.resolveToolNamesLocked(s.resources.ToolNames)
	}
	return s.resolveToolNamesLocked(s.toolNamesLocked())
}

func (s *AgentHarness) resourcesLocked() AgentHarnessResources {
	return AgentHarnessResources{
		Skills:          slices.Clone(s.resources.Skills),
		PromptTemplates: clonePromptTemplates(s.promptTemplates),
	}
}

func cloneHarnessResources(resources AgentHarnessResources) AgentHarnessResources {
	return AgentHarnessResources{
		Skills:          slices.Clone(resources.Skills),
		PromptTemplates: clonePromptTemplates(resources.PromptTemplates),
	}
}

func cloneRunEvent(event RunEvent) RunEvent {
	switch value := event.(type) {
	case CoreAgentEvent:
		return CoreAgentEvent(cloneAgentEvent(protocol.AgentEvent(value)))
	case ToolsUpdateEvent:
		value.ToolNames = slices.Clone(value.ToolNames)
		value.PreviousToolNames = slices.Clone(value.PreviousToolNames)
		value.ActiveToolNames = slices.Clone(value.ActiveToolNames)
		value.PreviousActiveToolNames = slices.Clone(value.PreviousActiveToolNames)
		return value
	case ResourcesUpdateEvent:
		value.Resources = cloneHarnessResources(value.Resources)
		value.PreviousResources = cloneHarnessResources(value.PreviousResources)
		return value
	case SessionBeforeCompactEvent:
		value.Preparation.Messages = protocol.CloneMessageList(value.Preparation.Messages)
		value.Preparation.TurnPrefix = protocol.CloneMessageList(value.Preparation.TurnPrefix)
		value.Preparation.FileOperations.Read = cloneStringSet(value.Preparation.FileOperations.Read)
		value.Preparation.FileOperations.Written = cloneStringSet(value.Preparation.FileOperations.Written)
		value.Preparation.FileOperations.Edited = cloneStringSet(value.Preparation.FileOperations.Edited)
		value.BranchEntries = cloneSessionEntries(value.BranchEntries)
		return value
	case SessionCompactEvent:
		value.CompactionEntry = cloneSessionEntry(value.CompactionEntry)
		return value
	case SessionBeforeTreeEvent:
		value.Preparation.EntriesToSummarize = cloneSessionEntries(value.Preparation.EntriesToSummarize)
		return value
	case SessionTreeEvent:
		if value.SummaryEntry != nil {
			entry := cloneSessionEntry(*value.SummaryEntry)
			value.SummaryEntry = &entry
		}
		return value
	default:
		return event
	}
}

func cloneSessionEntries(entries []session.Entry) []session.Entry {
	if entries == nil {
		return nil
	}
	cloned := make([]session.Entry, len(entries))
	for i, entry := range entries {
		cloned[i] = cloneSessionEntry(entry)
	}
	return cloned
}

func cloneSessionEntry(entry session.Entry) session.Entry {
	entry.Message = protocol.CloneMessage(entry.Message)
	entry.ToolNames = slices.Clone(entry.ToolNames)
	entry.Details = bytes.Clone(entry.Details)
	entry.Payload = bytes.Clone(entry.Payload)
	return entry
}

func cloneStringSet(values map[string]struct{}) map[string]struct{} {
	if values == nil {
		return nil
	}
	cloned := make(map[string]struct{}, len(values))
	for value := range values {
		cloned[value] = struct{}{}
	}
	return cloned
}

func cloneAgentEvents(events []protocol.AgentEvent) []protocol.AgentEvent {
	if len(events) == 0 {
		return nil
	}
	out := make([]protocol.AgentEvent, len(events))
	for i, event := range events {
		out[i] = cloneAgentEvent(event)
	}
	return out
}

func cloneAgentEvent(event protocol.AgentEvent) protocol.AgentEvent {
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
	return event
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

func validateQueuedMessages(messages protocol.MessageList) error {
	if len(messages) == 0 {
		return errors.New("queued messages are required")
	}
	return validateMessages(messages)
}

func validateMessages(messages protocol.MessageList) error {
	for _, message := range messages {
		if message == nil {
			return errors.New("message is required")
		}
		if err := message.Validate(); err != nil {
			return err
		}
	}
	return nil
}
