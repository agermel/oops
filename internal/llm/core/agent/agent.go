package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"oops/internal/llm/ai/protocol"
)

var ErrAgentBusy = errors.New("agent is busy")

type AgentOptions struct {
	Context AgentContext
	Config  AgentLoopConfig
}

type AgentListener func(context.Context, protocol.AgentEvent, AgentState) error

type Unsubscribe func()

type Agent struct {
	mu sync.Mutex

	state  AgentState
	config AgentLoopConfig

	nextListenerID int
	listenerOrder  []int
	listeners      map[int]AgentListener

	active *activeRun
}

type activeRun struct {
	observedAgentEnd bool
}

type listenerDispatchError struct {
	err error
}

func (e listenerDispatchError) Error() string { return e.err.Error() }
func (e listenerDispatchError) Unwrap() error { return e.err }

func NewAgent(options AgentOptions) *Agent {
	return &Agent{
		state:     newAgentState(options.Context, options.Config),
		config:    options.Config,
		listeners: map[int]AgentListener{},
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
	return a.runPrompt(ctx, prompts)
}

func (a *Agent) runPrompt(ctx context.Context, messages protocol.MessageList) (protocol.MessageList, error) {
	a.mu.Lock()
	if a.active != nil {
		a.mu.Unlock()
		return nil, ErrAgentBusy
	}
	return a.runPromptLocked(ctx, messages)
}

func (a *Agent) runPromptLocked(ctx context.Context, messages protocol.MessageList) (protocol.MessageList, error) {
	active, snapshot, config, emit := a.beginRunLocked()
	a.mu.Unlock()

	result, err := RunAgentLoop(ctx, messages, snapshot, config, emit)
	result, err = a.maybeEmitFailure(ctx, emit, config, result, err)
	a.finishRun(active)
	return cloneMessages(result), err
}

func (a *Agent) beginRunLocked() (*activeRun, AgentContext, AgentLoopConfig, EventSink) {
	active := &activeRun{}
	a.active = active
	a.state.IsStreaming = true
	a.state.Phase = AgentPhaseStreaming
	a.state.StreamingMessage = nil
	a.state.PendingToolCalls = nil
	a.state.ErrorMessage = ""
	snapshot := a.contextSnapshotLocked()
	config := a.loopConfigForRunLocked()
	emit := a.eventSink()
	return active, snapshot, config, emit
}

func (a *Agent) contextSnapshotLocked() AgentContext {
	return AgentContext{
		SystemPrompt: a.state.SystemPrompt,
		Messages:     cloneMessages(a.state.Messages),
		Tools:        cloneTools(a.state.Tools),
	}
}

func (a *Agent) loopConfigForRunLocked() AgentLoopConfig {
	config := a.config
	config.Model = a.state.Model
	config.Provider = a.state.Provider
	config.Reasoning = a.state.Reasoning
	return config
}

func cloneValidatedMessages(messages protocol.MessageList) (protocol.MessageList, error) {
	for _, message := range messages {
		if message == nil {
			return nil, fmt.Errorf("message list contains nil message")
		}
		if err := message.Validate(); err != nil {
			return nil, err
		}
	}
	return cloneMessages(messages), nil
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

func (a *Agent) finishRun(active *activeRun) {
	a.mu.Lock()
	a.state.IsStreaming = false
	a.state.Phase = AgentPhaseIdle
	a.state.StreamingMessage = nil
	a.state.PendingToolCalls = nil
	if a.active == active {
		a.active = nil
	}
	a.mu.Unlock()
}
