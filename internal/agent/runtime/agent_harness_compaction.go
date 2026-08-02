package runtime

import (
	"context"
	"errors"
	"fmt"

	protocol "oops/internal/agent/ai"
	agentcore "oops/internal/agent/core"
	"oops/internal/agent/runtime/compaction"
	"oops/internal/agent/runtime/session"
)

type summaryOperationInputs struct {
	model     protocol.Model
	reasoning string
	stream    protocol.StreamFunc
	sessionID string
}

func (s *AgentHarness) Compact(ctx context.Context, customInstructions string) (result compaction.Result, err error) {
	inputs, err := s.beginSummaryOperation(AgentHarnessPhaseCompaction)
	if err != nil {
		return compaction.Result{}, err
	}
	defer func() {
		err = errors.Join(err, s.finishSummaryOperation())
	}()

	if inputs.model.ID == "" {
		return compaction.Result{}, errors.New("compaction requires model")
	}
	if inputs.stream == nil {
		return compaction.Result{}, errors.New("compaction requires model stream")
	}
	preparation, err := compaction.Prepare(s.session, compaction.DefaultSettings)
	if err != nil {
		return compaction.Result{}, err
	}
	if preparation == nil {
		return compaction.Result{}, errors.New("nothing to compact")
	}
	branchEntries, _, err := s.session.EntriesForBranchSummary("")
	if err != nil {
		return compaction.Result{}, err
	}
	if err := s.emitRunEvent(ctx, SessionBeforeCompactEvent{
		Type:               "session_before_compact",
		Preparation:        *preparation,
		BranchEntries:      branchEntries,
		CustomInstructions: customInstructions,
	}); err != nil {
		return compaction.Result{}, err
	}

	result, err = compaction.Compact(ctx, *preparation, compaction.CompactOptions{
		Completer:          streamCompleter(inputs.stream, inputs.sessionID),
		Model:              inputs.model,
		CustomInstructions: customInstructions,
		Reasoning:          inputs.reasoning,
	})
	if err != nil {
		return compaction.Result{}, err
	}
	entry, err := s.repo.AppendCompaction(
		inputs.sessionID,
		result.Summary,
		result.FirstKeptEntryID,
		result.TokensBefore,
		result.Details,
	)
	if err != nil {
		return compaction.Result{}, err
	}
	if err := s.refreshSummaryOperation(); err != nil {
		return compaction.Result{}, err
	}
	if err := s.emitRunEvent(ctx, SessionCompactEvent{
		Type:            "session_compact",
		CompactionEntry: entry,
	}); err != nil {
		return compaction.Result{}, err
	}
	return result, nil
}

func (s *AgentHarness) NavigateTreeSnapshot(leafID string) (SessionSnapshot, error) {
	return s.NavigateTree(context.Background(), leafID, NavigateTreeOptions{})
}

func (s *AgentHarness) NavigateTreeWithSummarySnapshot(leafID, summary string) (SessionSnapshot, error) {
	return s.NavigateTree(context.Background(), leafID, NavigateTreeOptions{Summary: summary})
}

func (s *AgentHarness) NavigateTree(ctx context.Context, targetID string, options NavigateTreeOptions) (snapshot SessionSnapshot, err error) {
	if err := ctx.Err(); err != nil {
		return SessionSnapshot{}, err
	}
	inputs, err := s.beginSummaryOperation(AgentHarnessPhaseBranchSummary)
	if err != nil {
		return SessionSnapshot{}, err
	}
	editorText := ""
	defer func() {
		s.mu.Lock()
		settleErr := s.rebuildFromSessionLocked()
		s.phase = AgentHarnessPhaseIdle
		if err == nil && settleErr == nil {
			snapshot = s.snapshotLocked(editorText)
		}
		s.mu.Unlock()
		err = errors.Join(err, settleErr)
	}()

	oldLeafID := s.session.LeafID()
	target, err := s.session.ResolveNavigationTarget(targetID)
	if err != nil {
		return SessionSnapshot{}, err
	}
	editorText = target.EditorText
	entries, commonAncestorID, err := s.session.EntriesForBranchSummary(target.LeafID)
	if err != nil {
		return SessionSnapshot{}, err
	}
	preparation := TreePreparation{
		TargetID:            targetID,
		OldLeafID:           oldLeafID,
		CommonAncestorID:    commonAncestorID,
		EntriesToSummarize:  entries,
		UserWantsSummary:    options.Summarize,
		CustomInstructions:  options.CustomInstructions,
		ReplaceInstructions: options.ReplaceInstructions,
	}
	if err := s.emitRunEvent(ctx, SessionBeforeTreeEvent{
		Type:        "session_before_tree",
		Preparation: preparation,
	}); err != nil {
		return SessionSnapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		return SessionSnapshot{}, err
	}

	var summaryEntry *session.Entry
	summary := options.Summary
	var details session.SummaryDetails
	if summary != "" {
		details, err = s.session.BranchSummaryDetails(target.LeafID)
		if err != nil {
			return SessionSnapshot{}, err
		}
	} else if options.Summarize && len(entries) > 0 {
		if inputs.model.ID == "" {
			return SessionSnapshot{}, errors.New("branch summary requires model")
		}
		if inputs.stream == nil {
			return SessionSnapshot{}, errors.New("branch summary requires model stream")
		}
		generated, err := compaction.GenerateBranchSummary(ctx, entries, compaction.BranchOptions{
			Completer:           streamCompleter(inputs.stream, inputs.sessionID),
			Model:               inputs.model,
			CustomInstructions:  options.CustomInstructions,
			ReplaceInstructions: options.ReplaceInstructions,
		})
		if err != nil {
			return SessionSnapshot{}, err
		}
		summary = generated.Summary
		details = branchSummaryDetails(generated)
	}

	if summary != "" {
		entry, err := s.repo.AppendBranchSummary(inputs.sessionID, target.LeafID, summary, details)
		if err != nil {
			return SessionSnapshot{}, err
		}
		summaryEntry = &entry
	} else {
		if _, err := s.repo.AppendEntry(inputs.sessionID, session.Entry{Type: session.EntryLeaf, LeafID: target.LeafID}); err != nil {
			return SessionSnapshot{}, err
		}
	}
	if err := s.refreshSummaryOperation(); err != nil {
		return SessionSnapshot{}, err
	}
	if err := s.emitRunEvent(ctx, SessionTreeEvent{
		Type:         "session_tree",
		NewLeafID:    s.session.LeafID(),
		OldLeafID:    oldLeafID,
		SummaryEntry: summaryEntry,
	}); err != nil {
		return SessionSnapshot{}, err
	}
	return SessionSnapshot{}, nil
}

func (s *AgentHarness) beginSummaryOperation(phase AgentHarnessPhase) (summaryOperationInputs, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runMu.Lock()
	runActive := s.runDone != nil
	s.runMu.Unlock()
	if s.phase != AgentHarnessPhaseIdle || runActive {
		return summaryOperationInputs{}, agentcore.ErrAgentBusy
	}
	s.phase = phase
	if err := s.rebuildFromSessionLocked(); err != nil {
		s.phase = AgentHarnessPhaseIdle
		return summaryOperationInputs{}, err
	}
	current := s.session.BuildContext()
	selectedModel := protocol.Model{
		Provider: firstNonEmpty(current.Provider, s.provider),
		ID:       firstNonEmpty(current.Model, s.model),
	}
	if configured := s.providerClient.Model(); configured.Provider == selectedModel.Provider && configured.ID == selectedModel.ID {
		selectedModel.ContextWindow = configured.ContextWindow
		selectedModel.MaxTokens = configured.MaxTokens
	}
	return summaryOperationInputs{
		model:     selectedModel,
		reasoning: firstNonEmpty(current.Reasoning, s.reasoning),
		stream:    s.config.Stream,
		sessionID: s.session.ID(),
	}, nil
}

func (s *AgentHarness) refreshSummaryOperation() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rebuildFromSessionLocked()
}

func (s *AgentHarness) finishSummaryOperation() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.rebuildFromSessionLocked()
	s.phase = AgentHarnessPhaseIdle
	return err
}

func (s *AgentHarness) rebuildFromSessionLocked() error {
	if err := s.flushPendingSessionWritesLocked(); err != nil {
		return err
	}
	return s.rebuildAgentLocked()
}

func streamCompleter(stream protocol.StreamFunc, sessionID string) compaction.Completer {
	return func(ctx context.Context, request compaction.CompleteRequest) (protocol.AssistantMessage, error) {
		responseStream, err := stream(ctx, protocol.StreamRequest{
			Context:   request.Context,
			Model:     request.Model.ID,
			Provider:  request.Model.Provider,
			Reasoning: request.Reasoning,
			SessionID: sessionID,
			MaxTokens: request.MaxTokens,
		})
		if err != nil {
			return protocol.AssistantMessage{}, err
		}
		if responseStream == nil {
			return protocol.AssistantMessage{}, fmt.Errorf("model stream returned nil")
		}
		events := responseStream.Events()
		for events != nil {
			select {
			case _, open := <-events:
				if !open {
					events = nil
				}
			case <-ctx.Done():
				return protocol.AssistantMessage{}, ctx.Err()
			}
		}
		message, err := responseStream.Result(ctx)
		if err != nil {
			return protocol.AssistantMessage{}, err
		}
		if message == nil {
			return protocol.AssistantMessage{}, errors.New("model stream returned no result")
		}
		return *message, nil
	}
}

func branchSummaryDetails(result compaction.BranchResult) session.SummaryDetails {
	return session.SummaryDetails{
		ReadFiles:     result.ReadFiles,
		ModifiedFiles: result.ModifiedFiles,
	}
}
