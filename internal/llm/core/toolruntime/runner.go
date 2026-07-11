package toolruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"oops/internal/llm/ai/protocol"
)

type updateEmitError struct {
	err error
}

func (e updateEmitError) Error() string { return e.err.Error() }
func (e updateEmitError) Unwrap() error { return e.err }

type preparedCall struct {
	index      int
	tool       Tool
	call       protocol.ToolCallContent
	runtime    ToolCall
	registered registeredTool
}

type toolOutcome struct {
	message   protocol.ToolResultMessage
	terminate bool
}

func NewRunner(config RunnerConfig) (*Runner, error) {
	registry := config.Registry
	if registry == nil {
		var err error
		registry, err = NewRegistry(config.Tools)
		if err != nil {
			return nil, err
		}
	}
	mode := config.ExecutionMode
	if mode == "" {
		mode = ExecutionModeParallel
	}
	if mode != ExecutionModeParallel && mode != ExecutionModeSequential {
		return nil, fmt.Errorf("unknown execution mode %q", mode)
	}
	return &Runner{registry: registry, mode: mode, hooks: config.Hooks}, nil
}

func (r *Runner) ExecuteTools(ctx context.Context, req ToolRunRequest) (ToolRunResult, error) {
	if r == nil || r.registry == nil {
		return ToolRunResult{}, errors.New("tool runner is not configured")
	}
	if len(req.ToolCalls) == 0 {
		return ToolRunResult{}, nil
	}
	if r.batchMode(req.ToolCalls) == ExecutionModeSequential {
		return r.executeSequential(ctx, req)
	}
	return r.executeParallel(ctx, req)
}

func (r *Runner) batchMode(calls []protocol.ToolCallContent) ExecutionMode {
	if r.mode == ExecutionModeSequential {
		return ExecutionModeSequential
	}
	for _, call := range calls {
		item, ok := r.registry.lookup(call.Name)
		if ok && item.tool.ExecutionMode() == ExecutionModeSequential {
			return ExecutionModeSequential
		}
	}
	return ExecutionModeParallel
}

func (r *Runner) executeSequential(ctx context.Context, req ToolRunRequest) (ToolRunResult, error) {
	outcomes := make([]toolOutcome, 0, len(req.ToolCalls))
	terminateCount := 0
	var emitMu sync.Mutex
	for i, call := range req.ToolCalls {
		if err := ctx.Err(); err != nil {
			return ToolRunResult{Messages: outcomeMessages(outcomes)}, err
		}
		prepared, outcome, ready, err := r.prepare(ctx, req, i, call, &emitMu)
		if err != nil {
			return ToolRunResult{}, err
		}
		if ready {
			outcome, err = r.executePrepared(ctx, req, prepared, &emitMu)
			if err != nil {
				return ToolRunResult{}, err
			}
		}
		if err := emitToolMessage(ctx, req.Emit, req.Turn, outcome.message); err != nil {
			return ToolRunResult{}, err
		}
		if outcome.terminate {
			terminateCount++
		}
		outcomes = append(outcomes, outcome)
	}
	return ToolRunResult{
		Messages:  outcomeMessages(outcomes),
		Terminate: len(outcomes) > 0 && terminateCount == len(outcomes),
	}, nil
}

func (r *Runner) executeParallel(ctx context.Context, req ToolRunRequest) (ToolRunResult, error) {
	outcomes := make([]toolOutcome, len(req.ToolCalls))
	ready := make([]preparedCall, 0, len(req.ToolCalls))
	var emitMu sync.Mutex
	for i, call := range req.ToolCalls {
		if err := ctx.Err(); err != nil {
			return ToolRunResult{}, err
		}
		prepared, outcome, ok, err := r.prepare(ctx, req, i, call, &emitMu)
		if err != nil {
			return ToolRunResult{}, err
		}
		if !ok {
			outcomes[i] = outcome
			continue
		}
		ready = append(ready, prepared)
	}

	var wg sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error
	for _, item := range ready {
		item := item
		wg.Add(1)
		go func() {
			defer wg.Done()
			outcome, err := r.executePrepared(ctx, req, item, &emitMu)
			if err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errMu.Unlock()
				return
			}
			outcomes[item.index] = outcome
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return ToolRunResult{}, firstErr
	}
	if err := ctx.Err(); err != nil {
		return ToolRunResult{}, err
	}

	terminateCount := 0
	for _, outcome := range outcomes {
		if outcome.terminate {
			terminateCount++
		}
		if err := emitToolMessage(ctx, req.Emit, req.Turn, outcome.message); err != nil {
			return ToolRunResult{}, err
		}
	}
	return ToolRunResult{
		Messages:  outcomeMessages(outcomes),
		Terminate: len(outcomes) > 0 && terminateCount == len(outcomes),
	}, nil
}

func (r *Runner) prepare(
	ctx context.Context,
	req ToolRunRequest,
	index int,
	call protocol.ToolCallContent,
	emitMu *sync.Mutex,
) (preparedCall, toolOutcome, bool, error) {
	call = protocol.CloneToolCallContent(call)
	if len(call.Arguments) == 0 {
		call.Arguments = json.RawMessage(`{}`)
	}
	if err := emitToolEvent(ctx, emitMu, req.Emit, protocol.AgentEvent{
		Type:       protocol.AgentEventToolExecutionStart,
		Turn:       req.Turn,
		ToolCallID: call.ID,
		ToolName:   call.Name,
		Args:       protocol.CloneToolCallContent(call).Arguments,
	}); err != nil {
		return preparedCall{}, toolOutcome{}, false, err
	}
	item, ok := r.registry.lookup(call.Name)
	if !ok {
		outcome := makeErrorOutcome(call, fmt.Sprintf("unknown tool %q", call.Name))
		if err := emitToolEnd(ctx, emitMu, req.Emit, req.Turn, outcome); err != nil {
			return preparedCall{}, toolOutcome{}, false, err
		}
		return preparedCall{}, outcome, false, nil
	}

	raw := cloneRaw(call.Arguments)
	preparedRaw, err := item.tool.PrepareArguments(ctx, raw)
	if err != nil {
		outcome := makeErrorOutcome(call, err.Error())
		if err := emitToolEnd(ctx, emitMu, req.Emit, req.Turn, outcome); err != nil {
			return preparedCall{}, toolOutcome{}, false, err
		}
		return preparedCall{}, outcome, false, nil
	}
	if len(preparedRaw) == 0 {
		preparedRaw = json.RawMessage(`{}`)
	}
	args, err := decodeArguments(preparedRaw)
	if err != nil {
		outcome := makeErrorOutcome(call, err.Error())
		if err := emitToolEnd(ctx, emitMu, req.Emit, req.Turn, outcome); err != nil {
			return preparedCall{}, toolOutcome{}, false, err
		}
		return preparedCall{}, outcome, false, nil
	}
	if err := validateWithSchema(item.schema, args); err != nil {
		outcome := makeErrorOutcome(call, err.Error())
		if err := emitToolEnd(ctx, emitMu, req.Emit, req.Turn, outcome); err != nil {
			return preparedCall{}, toolOutcome{}, false, err
		}
		return preparedCall{}, outcome, false, nil
	}

	if r.hooks.BeforeToolCall != nil {
		hookResult, err := r.hooks.BeforeToolCall(ctx, BeforeToolCallContext{
			AssistantMessage: protocol.CloneAssistantMessage(req.AssistantMessage),
			ToolCall:         protocol.CloneToolCallContent(call),
			Arguments:        cloneArguments(args),
			RawArguments:     cloneRaw(preparedRaw),
			Context:          snapshotContext(req.Context),
		})
		if err != nil {
			outcome := makeErrorOutcome(call, err.Error())
			if err := emitToolEnd(ctx, emitMu, req.Emit, req.Turn, outcome); err != nil {
				return preparedCall{}, toolOutcome{}, false, err
			}
			return preparedCall{}, outcome, false, nil
		}
		if len(hookResult.Arguments) > 0 {
			preparedRaw = cloneRaw(hookResult.Arguments)
			args, err = decodeArguments(preparedRaw)
			if err != nil {
				outcome := makeErrorOutcome(call, err.Error())
				if err := emitToolEnd(ctx, emitMu, req.Emit, req.Turn, outcome); err != nil {
					return preparedCall{}, toolOutcome{}, false, err
				}
				return preparedCall{}, outcome, false, nil
			}
			if err := validateWithSchema(item.schema, args); err != nil {
				outcome := makeErrorOutcome(call, err.Error())
				if err := emitToolEnd(ctx, emitMu, req.Emit, req.Turn, outcome); err != nil {
					return preparedCall{}, toolOutcome{}, false, err
				}
				return preparedCall{}, outcome, false, nil
			}
		}
		if hookResult.Block {
			reason := hookResult.Reason
			if reason == "" {
				reason = "tool call blocked"
			}
			outcome := makeErrorOutcome(call, reason)
			if err := emitToolEnd(ctx, emitMu, req.Emit, req.Turn, outcome); err != nil {
				return preparedCall{}, toolOutcome{}, false, err
			}
			return preparedCall{}, outcome, false, nil
		}
	}

	call.Arguments = cloneRaw(preparedRaw)
	return preparedCall{
		index:      index,
		tool:       item.tool,
		call:       call,
		registered: item,
		runtime: ToolCall{
			ID:           call.ID,
			Name:         call.Name,
			RawArguments: cloneRaw(preparedRaw),
			Arguments:    cloneArguments(args),
		},
	}, toolOutcome{}, true, nil
}

func (r *Runner) executePrepared(ctx context.Context, req ToolRunRequest, item preparedCall, emitMu *sync.Mutex) (toolOutcome, error) {
	if err := ctx.Err(); err != nil {
		return toolOutcome{}, err
	}
	state := &updateState{active: true}
	updateSink := func(updateCtx context.Context, event protocol.AgentEvent) error {
		event.Type = protocol.AgentEventToolExecutionUpdate
		event.Turn = req.Turn
		if event.ToolCallID == "" {
			event.ToolCallID = item.call.ID
		}
		if event.ToolName == "" {
			event.ToolName = item.call.Name
		}
		if err := state.emitIfActive(updateCtx, emitMu, req.Emit, event); err != nil {
			return updateEmitError{err: err}
		}
		return nil
	}
	result, err := item.tool.Execute(ctx, item.runtime, updateSink)
	isError := false
	if err != nil {
		var updateErr updateEmitError
		if errors.As(err, &updateErr) {
			state.close()
			return toolOutcome{}, updateErr.err
		}
		result = protocol.ToolResult{Content: protocol.ContentList{protocol.NewTextContent(err.Error())}}
		isError = true
	}
	if len(result.Content) == 0 {
		result.Content = protocol.ContentList{protocol.NewTextContent("")}
	}

	if r.hooks.AfterToolCall != nil {
		hookResult, hookErr := r.hooks.AfterToolCall(ctx, AfterToolCallContext{
			AssistantMessage: protocol.CloneAssistantMessage(req.AssistantMessage),
			ToolCall:         protocol.CloneToolCallContent(item.call),
			Arguments:        cloneArguments(item.runtime.Arguments),
			RawArguments:     cloneRaw(item.runtime.RawArguments),
			Result:           protocol.CloneToolResult(result),
			IsError:          isError,
			Context:          snapshotContext(req.Context),
		})
		if hookErr != nil {
			result = protocol.ToolResult{Content: protocol.ContentList{protocol.NewTextContent(hookErr.Error())}}
			isError = true
		} else {
			if hookResult.HasContent {
				result.Content = protocol.CloneContentList(hookResult.Content)
			}
			if hookResult.HasDetails {
				result.Details = cloneJSONLike(hookResult.Details)
			}
			if hookResult.IsError != nil {
				isError = *hookResult.IsError
			}
			if hookResult.Terminate != nil {
				result.Terminate = *hookResult.Terminate
			}
		}
	}

	outcome := makeOutcome(item.call, result, isError)
	state.close()
	if err := emitToolEnd(ctx, emitMu, req.Emit, req.Turn, outcome); err != nil {
		return toolOutcome{}, err
	}
	return outcome, nil
}

type updateState struct {
	mu     sync.Mutex
	active bool
}

func (s *updateState) emitIfActive(ctx context.Context, emitMu *sync.Mutex, emit EventSink, event protocol.AgentEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return nil
	}
	return emitToolEvent(ctx, emitMu, emit, event)
}

func (s *updateState) close() {
	s.mu.Lock()
	s.active = false
	s.mu.Unlock()
}

func emitToolEnd(ctx context.Context, emitMu *sync.Mutex, emit EventSink, turn int, outcome toolOutcome) error {
	result := protocol.ToolResult{
		Content:   protocol.CloneContentList(outcome.message.Content),
		Details:   cloneJSONLike(outcome.message.Details),
		Terminate: outcome.terminate,
	}
	return emitToolEvent(ctx, emitMu, emit, protocol.AgentEvent{
		Type:       protocol.AgentEventToolExecutionEnd,
		Turn:       turn,
		ToolCallID: outcome.message.ToolCallID,
		ToolName:   outcome.message.ToolName,
		Result:     &result,
		IsError:    outcome.message.IsError,
	})
}

func emitToolEvent(ctx context.Context, emitMu *sync.Mutex, emit EventSink, event protocol.AgentEvent) error {
	if emit == nil {
		return nil
	}
	emitMu.Lock()
	defer emitMu.Unlock()
	return emit(ctx, event)
}

func emitToolMessage(ctx context.Context, emit EventSink, turn int, message protocol.ToolResultMessage) error {
	if emit == nil {
		return nil
	}
	if err := emit(ctx, protocol.AgentEvent{Type: protocol.AgentEventMessageStart, Turn: turn, Message: message}); err != nil {
		return err
	}
	return emit(ctx, protocol.AgentEvent{Type: protocol.AgentEventMessageEnd, Turn: turn, Message: message})
}

func makeErrorOutcome(call protocol.ToolCallContent, message string) toolOutcome {
	return makeOutcome(call, protocol.ToolResult{Content: protocol.ContentList{protocol.NewTextContent(message)}}, true)
}

func makeOutcome(call protocol.ToolCallContent, result protocol.ToolResult, isError bool) toolOutcome {
	return toolOutcome{
		message: protocol.ToolResultMessage{
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Content:    protocol.CloneContentList(result.Content),
			Details:    cloneJSONLike(result.Details),
			IsError:    isError,
		},
		terminate: result.Terminate,
	}
}

func outcomeMessages(outcomes []toolOutcome) []protocol.ToolResultMessage {
	if len(outcomes) == 0 {
		return nil
	}
	messages := make([]protocol.ToolResultMessage, len(outcomes))
	for i, outcome := range outcomes {
		messages[i] = protocol.CloneToolResultMessage(outcome.message)
	}
	return messages
}

func decodeArguments(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if out == nil {
		return nil, errors.New("tool call arguments must be a json object")
	}
	return out, nil
}

func snapshotContext(value ContextSnapshot) ContextSnapshot {
	return ContextSnapshot{
		SystemPrompt: value.SystemPrompt,
		Messages:     protocol.CloneMessageList(value.Messages),
		Tools:        protocol.CloneTools(value.Tools),
	}
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	return bytes.Clone(raw)
}

func cloneArguments(args map[string]any) map[string]any {
	if args == nil {
		return nil
	}
	data, err := json.Marshal(args)
	if err != nil {
		out := make(map[string]any, len(args))
		for key, value := range args {
			out[key] = value
		}
		return out
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		out := make(map[string]any, len(args))
		for key, value := range args {
			out[key] = value
		}
		return out
	}
	return out
}

func cloneJSONLike(value any) any {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return value
	}
	return out
}
