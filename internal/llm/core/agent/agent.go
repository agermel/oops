package agent

import (
	"context"
	"errors"
	"sync"

	"oops/internal/llm/ai/protocol"
)

var ErrAgentBusy = errors.New("agent is busy")

type AgentOptions struct {
	Context      AgentContext
	Config       AgentLoopConfig
	SteeringMode QueueMode
	FollowUpMode QueueMode
}

type AgentListener func(context.Context, protocol.AgentEvent, AgentState) error

type Unsubscribe func()

type Agent struct {
	mu sync.Mutex

	state  AgentState
	config AgentLoopConfig

	steeringQueue messageQueue
	followUpQueue messageQueue

	nextListenerID int
	listenerOrder  []int
	listeners      map[int]AgentListener

	active *activeRun
}

type activeRun struct {
	cancel           context.CancelFunc
	done             chan struct{}
	result           protocol.MessageList
	err              error
	observedAgentEnd bool
}

type listenerDispatchError struct {
	err error
}

func (e listenerDispatchError) Error() string { return e.err.Error() }
func (e listenerDispatchError) Unwrap() error { return e.err }

func NewAgent(options AgentOptions) *Agent {
	steeringMode := options.SteeringMode
	if steeringMode == "" {
		steeringMode = QueueModeOneAtATime
	}
	followUpMode := options.FollowUpMode
	if followUpMode == "" {
		followUpMode = QueueModeOneAtATime
	}
	return &Agent{
		state:         newAgentState(options.Context, options.Config),
		config:        options.Config,
		steeringQueue: newMessageQueue(steeringMode),
		followUpQueue: newMessageQueue(followUpMode),
		listeners:     map[int]AgentListener{},
	}
}

func (a *Agent) State() AgentState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return cloneAgentState(a.state)
}

func (a *Agent) Listen(listener AgentListener) Unsubscribe {
	if listener == nil {
		return func() {}
	}
	a.mu.Lock()
	id := a.nextListenerID
	a.nextListenerID++
	a.listeners[id] = listener
	a.listenerOrder = append(a.listenerOrder, id)
	a.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			a.mu.Lock()
			delete(a.listeners, id)
			for i, value := range a.listenerOrder {
				if value == id {
					a.listenerOrder = append(a.listenerOrder[:i], a.listenerOrder[i+1:]...)
					break
				}
			}
			a.mu.Unlock()
		})
	}
}

func (a *Agent) Prompt(ctx context.Context, messages protocol.MessageList) (protocol.MessageList, error) {
	prompts, err := cloneValidatedMessages(messages)
	if err != nil {
		return nil, err
	}
	return a.runPrompt(ctx, prompts, false)
}

func (a *Agent) Continue(ctx context.Context) (protocol.MessageList, error) {
	a.mu.Lock()
	if a.active != nil {
		a.mu.Unlock()
		return nil, ErrAgentBusy
	}
	if len(a.state.Messages) == 0 {
		a.mu.Unlock()
		return nil, ErrContinueEmptyContext
	}
	last := a.state.Messages[len(a.state.Messages)-1]
	if last.MessageRole() == protocol.RoleAssistant {
		if queued := a.steeringQueue.drain(); len(queued) > 0 {
			return a.runPromptLocked(ctx, queued, true)
		}
		if queued := a.followUpQueue.drain(); len(queued) > 0 {
			return a.runPromptLocked(ctx, queued, false)
		}
		a.mu.Unlock()
		return nil, ErrContinueFromAssistant
	}
	return a.runContinueLocked(ctx)
}

func (a *Agent) Steer(messages protocol.MessageList) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.steeringQueue.enqueue(messages)
}

func (a *Agent) FollowUp(messages protocol.MessageList) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.followUpQueue.enqueue(messages)
}

func (a *Agent) ClearAllQueues() {
	a.mu.Lock()
	a.steeringQueue.clear()
	a.followUpQueue.clear()
	a.mu.Unlock()
}

func (a *Agent) HasQueuedMessages() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.steeringQueue.hasItems() || a.followUpQueue.hasItems()
}

func (a *Agent) Abort() bool {
	a.mu.Lock()
	active := a.active
	if active == nil {
		a.mu.Unlock()
		return false
	}
	a.state.Phase = AgentPhaseAborted
	a.state.IsStreaming = true
	cancel := active.cancel
	a.mu.Unlock()
	cancel()
	return true
}

func (a *Agent) WaitForIdle(ctx context.Context) error {
	a.mu.Lock()
	active := a.active
	if active == nil {
		a.mu.Unlock()
		return nil
	}
	done := active.done
	a.mu.Unlock()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *Agent) runPrompt(ctx context.Context, messages protocol.MessageList, skipInitialSteeringPoll bool) (protocol.MessageList, error) {
	a.mu.Lock()
	if a.active != nil {
		a.mu.Unlock()
		return nil, ErrAgentBusy
	}
	return a.runPromptLocked(ctx, messages, skipInitialSteeringPoll)
}

func (a *Agent) runPromptLocked(ctx context.Context, messages protocol.MessageList, skipInitialSteeringPoll bool) (protocol.MessageList, error) {
	active, runCtx, snapshot, config, emit := a.beginRunLocked(ctx, skipInitialSteeringPoll)
	a.mu.Unlock()

	result, err := RunAgentLoop(runCtx, messages, snapshot, config, emit)
	result, err = a.maybeEmitFailure(runCtx, emit, config, result, err)
	a.finishRun(active, result, err)
	return cloneMessages(result), err
}

func (a *Agent) runContinueLocked(ctx context.Context) (protocol.MessageList, error) {
	active, runCtx, snapshot, config, emit := a.beginRunLocked(ctx, false)
	a.mu.Unlock()

	result, err := RunAgentLoopContinue(runCtx, snapshot, config, emit)
	result, err = a.maybeEmitFailure(runCtx, emit, config, result, err)
	a.finishRun(active, result, err)
	return cloneMessages(result), err
}

func (a *Agent) beginRunLocked(ctx context.Context, skipInitialSteeringPoll bool) (*activeRun, context.Context, AgentContext, AgentLoopConfig, EventSink) {
	runCtx, cancel := context.WithCancel(ctx)
	active := &activeRun{cancel: cancel, done: make(chan struct{})}
	a.active = active
	a.state.IsStreaming = true
	a.state.Phase = AgentPhaseStreaming
	a.state.StreamingMessage = nil
	a.state.PendingToolCalls = nil
	a.state.ErrorMessage = ""
	snapshot := a.contextSnapshotLocked()
	config := a.loopConfigForRunLocked(skipInitialSteeringPoll)
	emit := a.eventSink()
	return active, runCtx, snapshot, config, emit
}

func (a *Agent) contextSnapshotLocked() AgentContext {
	return AgentContext{
		SystemPrompt: a.state.SystemPrompt,
		Messages:     cloneMessages(a.state.Messages),
		Tools:        cloneTools(a.state.Tools),
	}
}

func (a *Agent) loopConfigForRunLocked(skipInitialSteeringPoll bool) AgentLoopConfig {
	config := a.config
	config.Model = a.state.Model
	config.Provider = a.state.Provider
	config.Reasoning = a.state.Reasoning
	originalSteering := config.GetSteeringMessages
	originalFollowUp := config.GetFollowUpMessages
	skipSteering := skipInitialSteeringPoll
	config.GetSteeringMessages = func(ctx context.Context) (protocol.MessageList, error) {
		if skipSteering {
			skipSteering = false
			return nil, nil
		}
		a.mu.Lock()
		messages := a.steeringQueue.drain()
		a.mu.Unlock()
		if len(messages) > 0 || originalSteering == nil {
			return messages, nil
		}
		return originalSteering(ctx)
	}
	config.GetFollowUpMessages = func(ctx context.Context) (protocol.MessageList, error) {
		a.mu.Lock()
		messages := a.followUpQueue.drain()
		a.mu.Unlock()
		if len(messages) > 0 || originalFollowUp == nil {
			return messages, nil
		}
		return originalFollowUp(ctx)
	}
	return config
}

func (a *Agent) eventSink() EventSink {
	return func(ctx context.Context, event protocol.AgentEvent) error {
		return a.processEvent(ctx, event)
	}
}

func (a *Agent) processEvent(ctx context.Context, event protocol.AgentEvent) error {
	a.mu.Lock()
	a.reduceAgentEventLocked(event)
	snapshot := cloneAgentState(a.state)
	listeners := a.listenerSnapshotLocked()
	a.mu.Unlock()

	var listenerErrors []error
	for _, listener := range listeners {
		if err := listener(ctx, event, snapshot); err != nil {
			listenerErrors = append(listenerErrors, err)
		}
	}
	if len(listenerErrors) > 0 {
		return listenerDispatchError{err: errors.Join(listenerErrors...)}
	}
	return nil
}

func (a *Agent) listenerSnapshotLocked() []AgentListener {
	if len(a.listenerOrder) == 0 {
		return nil
	}
	listeners := make([]AgentListener, 0, len(a.listenerOrder))
	for _, id := range a.listenerOrder {
		if listener := a.listeners[id]; listener != nil {
			listeners = append(listeners, listener)
		}
	}
	return listeners
}

func (a *Agent) maybeEmitFailure(ctx context.Context, emit EventSink, config AgentLoopConfig, result protocol.MessageList, err error) (protocol.MessageList, error) {
	if err == nil {
		return result, nil
	}
	var listenerErr listenerDispatchError
	if errors.As(err, &listenerErr) {
		return result, err
	}
	a.mu.Lock()
	observedAgentEnd := a.active == nil || a.active.observedAgentEnd
	a.mu.Unlock()
	if observedAgentEnd {
		return result, err
	}

	reason := protocol.StopReasonError
	if errors.Is(err, context.Canceled) || ctx.Err() != nil {
		reason = protocol.StopReasonAborted
	}
	message := newAssistantError(config.Provider, config.Model, reason, err.Error())
	if err := emit(ctx, protocol.AgentEvent{Type: protocol.AgentEventMessageStart, Turn: 1, Message: message}); err != nil {
		return result, err
	}
	if err := emit(ctx, protocol.AgentEvent{Type: protocol.AgentEventMessageEnd, Turn: 1, Message: message}); err != nil {
		return result, err
	}
	if err := emit(ctx, protocol.AgentEvent{Type: protocol.AgentEventTurnEnd, Turn: 1, Message: message}); err != nil {
		return result, err
	}
	newMessages := append(cloneMessages(result), message)
	if err := emit(ctx, protocol.AgentEvent{Type: protocol.AgentEventAgentEnd, Messages: cloneMessages(newMessages)}); err != nil {
		return result, err
	}
	return cloneMessages(newMessages), err
}

func (a *Agent) finishRun(active *activeRun, result protocol.MessageList, err error) {
	a.mu.Lock()
	a.state.IsStreaming = false
	a.state.Phase = AgentPhaseIdle
	a.state.StreamingMessage = nil
	a.state.PendingToolCalls = nil
	if a.active == active {
		a.active = nil
	}
	active.result = cloneMessages(result)
	active.err = err
	close(active.done)
	a.mu.Unlock()
}
