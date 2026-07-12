package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"oops/internal/config"
	"oops/internal/llm/ai/protocol"
	"oops/internal/llm/runtime/harness"
	runtimesession "oops/internal/llm/runtime/session"
)

var (
	ErrRunCapacity         = errors.New("run capacity exhausted")
	ErrRunManagerQuiescing = errors.New("run manager is quiescing")
	ErrRunSubscriberLimit  = errors.New("run subscriber limit reached")
	ErrRunReservationUsed  = errors.New("run reservation already released")
	ErrSessionBusy         = errors.New("session is busy")
)

type runDoneEvent struct {
	Type    string                  `json:"type"`
	Session harness.SessionSnapshot `json:"session"`
}

type runErrorEvent struct {
	Type    string                  `json:"type"`
	Error   string                  `json:"error"`
	Session harness.SessionSnapshot `json:"session"`
}

type runStreamItem struct {
	name    string
	payload any
}

// runManager owns run admission, replay retention, subscriptions and expiry.
// Its active counter includes reservations so expensive session construction
// cannot bypass the configured capacity limit.
type runManager struct {
	mu            sync.Mutex
	runs          map[string]*runState
	sessionLeases map[string]*sessionLease
	limits        config.RunLimits
	active        int
	quiescing     bool
	lifecycle     context.Context
	cancel        context.CancelFunc
	workers       sync.WaitGroup
	closeOnce     sync.Once
}

type runReservation struct {
	mu           sync.Mutex
	manager      *runManager
	reserved     bool
	sessionLease *sessionLease
}

type sessionLease struct {
	manager   *runManager
	sessionID string
	once      sync.Once
}

type runState struct {
	manager   *runManager
	limits    config.RunLimits
	mu        sync.Mutex
	id        string
	sessionID string
	projectID string
	cancel    context.CancelFunc
	lease     *sessionLease

	nextSequence   uint64
	history        []runFrame
	normalEvents   int
	normalBytes    int
	liveQueueBytes int
	subscribers    map[*runSubscriber]struct{}
	done           bool
	completedOnce  sync.Once
	executionOnce  sync.Once
}

// runFrame is fully encoded before it enters a run. Its byte slice remains
// immutable throughout replay and live delivery.
type runFrame struct {
	sequence uint64
	data     []byte
}

func (f runFrame) size() int {
	return len(f.data)
}

type runSubscriber struct {
	queue         chan runFrame
	pendingEvents int
	pendingBytes  int
	attached      bool
	closeOnce     sync.Once
}

type runSubscription struct {
	run         *runState
	history     []runFrame
	historyNext int
	subscriber  *runSubscriber
	queue       <-chan runFrame
	closeOnce   sync.Once
}

func newRunManager(configured ...config.RunLimits) *runManager {
	limits := config.DefaultRunLimits()
	if len(configured) > 0 {
		limits = configured[0].WithDefaults()
	}
	lifecycle, cancel := context.WithCancel(context.Background())
	return &runManager{
		runs:          make(map[string]*runState),
		sessionLeases: make(map[string]*sessionLease),
		limits:        limits,
		lifecycle:     lifecycle,
		cancel:        cancel,
	}
}

func (m *runManager) context() context.Context {
	return m.lifecycle
}

func (m *runManager) retryAfterHeader() string {
	seconds := int((m.limits.RetryAfter + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	return strconv.Itoa(seconds)
}

func (m *runManager) reserve() (*runReservation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.quiescing {
		return nil, ErrRunManagerQuiescing
	}
	if m.active >= m.limits.MaxActiveRuns {
		return nil, ErrRunCapacity
	}
	m.active++
	m.workers.Add(1)
	return &runReservation{manager: m, reserved: true}, nil
}

func (r *runReservation) release() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if !r.reserved {
		r.mu.Unlock()
		return
	}
	r.reserved = false
	m := r.manager
	lease := r.sessionLease
	r.sessionLease = nil
	r.mu.Unlock()
	lease.release()
	m.releaseReservation()
}

func (r *runReservation) claimSession(sessionID string) error {
	if r == nil {
		return ErrRunReservationUsed
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.reserved {
		return ErrRunReservationUsed
	}
	if r.sessionLease != nil {
		if r.sessionLease.sessionID == sessionID {
			return nil
		}
		return fmt.Errorf("reservation already owns session %q", r.sessionLease.sessionID)
	}
	lease, err := r.manager.acquireSession(sessionID)
	if err != nil {
		return err
	}
	r.sessionLease = lease
	return nil
}

func (r *runReservation) activate(runID, sessionID, projectID string, cancel context.CancelFunc) (*runState, error) {
	if r == nil {
		return nil, ErrRunReservationUsed
	}
	if err := r.claimSession(sessionID); err != nil {
		return nil, err
	}
	r.mu.Lock()
	if !r.reserved {
		r.mu.Unlock()
		return nil, ErrRunReservationUsed
	}
	r.reserved = false
	m := r.manager
	lease := r.sessionLease
	r.sessionLease = nil
	r.mu.Unlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.quiescing {
		m.releaseSessionLeaseLocked(lease)
		m.releaseActiveLocked()
		m.workers.Done()
		return nil, ErrRunManagerQuiescing
	}
	if _, exists := m.runs[runID]; exists {
		m.releaseSessionLeaseLocked(lease)
		m.releaseActiveLocked()
		m.workers.Done()
		return nil, fmt.Errorf("run %q already exists", runID)
	}
	run := &runState{
		manager:     m,
		limits:      m.limits,
		id:          runID,
		sessionID:   sessionID,
		projectID:   projectID,
		cancel:      cancel,
		lease:       lease,
		subscribers: make(map[*runSubscriber]struct{}),
	}
	m.runs[runID] = run
	m.workers.Add(1)
	m.workers.Done()
	return run, nil
}

func (m *runManager) acquireSession(sessionID string) (*sessionLease, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session id is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.quiescing {
		return nil, ErrRunManagerQuiescing
	}
	if _, exists := m.sessionLeases[sessionID]; exists {
		return nil, ErrSessionBusy
	}
	lease := &sessionLease{manager: m, sessionID: sessionID}
	m.sessionLeases[sessionID] = lease
	return lease, nil
}

func (l *sessionLease) release() {
	if l == nil || l.manager == nil {
		return
	}
	l.once.Do(func() {
		l.manager.mu.Lock()
		l.manager.releaseSessionLeaseLocked(l)
		l.manager.mu.Unlock()
	})
}

func (m *runManager) releaseSessionLeaseLocked(lease *sessionLease) {
	if lease != nil && m.sessionLeases[lease.sessionID] == lease {
		delete(m.sessionLeases, lease.sessionID)
	}
}

func (m *runManager) releaseReservation() {
	m.mu.Lock()
	m.releaseActiveLocked()
	m.workers.Done()
	m.mu.Unlock()
}

func (m *runManager) releaseActiveLocked() {
	if m.active > 0 {
		m.active--
	}
}

func (m *runManager) get(runID string) (*runState, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.runs[runID]
	return run, ok
}

func (m *runManager) subscribe(runID string) (*runSubscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.runs[runID]
	if !ok {
		return nil, nil
	}
	return run.subscribeLocked()
}

func (m *runManager) abort(runID string) (bool, bool) {
	run, ok := m.get(runID)
	if !ok {
		return false, false
	}
	return run.abort(), true
}

func (m *runManager) quiesce() {
	m.mu.Lock()
	if m.quiescing {
		m.mu.Unlock()
		return
	}
	m.quiescing = true
	if m.cancel != nil {
		m.cancel()
	}
	cancels := make([]context.CancelFunc, 0, len(m.runs))
	for _, run := range m.runs {
		run.mu.Lock()
		if !run.done && run.cancel != nil {
			cancels = append(cancels, run.cancel)
		}
		run.mu.Unlock()
	}
	m.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (m *runManager) close(ctx context.Context) error {
	m.closeOnce.Do(m.quiesce)
	done := make(chan struct{})
	go func() {
		m.workers.Wait()
		close(done)
	}()
	select {
	case <-done:
		m.mu.Lock()
		m.runs = make(map[string]*runState)
		m.sessionLeases = make(map[string]*sessionLease)
		m.mu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *runManager) complete(run *runState) {
	run.completedOnce.Do(func() {
		m.mu.Lock()
		scheduleExpiry := !m.quiescing && m.limits.CompletedTTL > 0
		if scheduleExpiry {
			m.workers.Add(1)
		}
		m.mu.Unlock()
		if scheduleExpiry {
			go m.expireAfterTTL(run)
		}
	})
}

func (r *runState) finishExecution() {
	r.executionOnce.Do(func() {
		r.releaseSessionLease()
		r.manager.mu.Lock()
		r.manager.releaseActiveLocked()
		r.manager.workers.Done()
		r.manager.mu.Unlock()
	})
}

func (r *runState) releaseSessionLease() {
	r.mu.Lock()
	lease := r.lease
	r.lease = nil
	r.mu.Unlock()
	lease.release()
}

func (m *runManager) expireAfterTTL(run *runState) {
	defer m.workers.Done()
	timer := time.NewTimer(m.limits.CompletedTTL)
	defer timer.Stop()
	select {
	case <-m.lifecycle.Done():
		return
	case <-timer.C:
	}
	m.mu.Lock()
	if m.runs[run.id] == run {
		delete(m.runs, run.id)
	}
	m.mu.Unlock()
}

func (r *runState) subscribeLocked() (*runSubscription, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	history := append([]runFrame(nil), r.history...)
	if r.done {
		return &runSubscription{run: r, history: history}, nil
	}
	if len(r.subscribers) >= r.limits.MaxSubscribers {
		return nil, ErrRunSubscriberLimit
	}
	subscriber := &runSubscriber{
		queue:    make(chan runFrame, r.limits.MaxSubscriberQueueEvents),
		attached: true,
	}
	r.subscribers[subscriber] = struct{}{}
	return &runSubscription{
		run:        r,
		history:    history,
		subscriber: subscriber,
		queue:      subscriber.queue,
	}, nil
}

func (s *runSubscription) next(ctx context.Context) (runFrame, bool, bool) {
	if s.historyNext < len(s.history) {
		frame := s.history[s.historyNext]
		s.historyNext++
		return frame, false, true
	}
	if s.queue == nil {
		return runFrame{}, false, false
	}
	select {
	case frame, ok := <-s.queue:
		return frame, true, ok
	case <-ctx.Done():
		return runFrame{}, false, false
	}
}

func (s *runSubscription) acknowledge(frame runFrame) {
	if s.subscriber != nil {
		s.run.acknowledge(s.subscriber, frame.size())
	}
}

func (s *runSubscription) unsubscribe() {
	if s == nil || s.subscriber == nil {
		return
	}
	s.closeOnce.Do(func() {
		s.run.removeSubscriber(s.subscriber)
	})
}

func (r *runState) publish(item runStreamItem) {
	frame, err := encodeRunFrame(item)
	if err != nil {
		r.fail(fmt.Sprintf("encode run event: %v", err))
		return
	}
	if frame.size() > r.limits.MaxEventBytes {
		r.fail(fmt.Sprintf("run event exceeds %d byte limit", r.limits.MaxEventBytes))
		return
	}

	r.mu.Lock()
	if r.done {
		r.mu.Unlock()
		return
	}
	if r.normalEvents+1 > r.limits.MaxRetainedEvents || r.normalBytes+frame.size() > r.limits.MaxRetainedBytes {
		r.mu.Unlock()
		r.fail("run event history limit reached")
		return
	}
	r.nextSequence++
	frame.sequence = r.nextSequence
	r.history = append(r.history, frame)
	r.normalEvents++
	r.normalBytes += frame.size()
	r.enqueueToSubscribersLocked(frame)
	r.mu.Unlock()
}

func (r *runState) publishTerminal(item runStreamItem) {
	frame, err := encodeTerminalRunFrame(item, r.limits)
	if err != nil {
		frame, err = encodeTerminalRunFrame(runStreamItem{
			name: "run_error",
			payload: runErrorEvent{
				Type:    "run_error",
				Error:   "run terminal event could not be encoded",
				Session: boundedRunSnapshot(harness.SessionSnapshot{SessionID: r.sessionID}, 0, 0),
			},
		}, r.limits)
		if err != nil {
			return
		}
	}

	r.mu.Lock()
	if r.done {
		r.mu.Unlock()
		return
	}
	r.nextSequence++
	frame.sequence = r.nextSequence
	r.history = append(r.history, frame)
	r.done = true
	cancel := r.cancel
	r.cancel = nil
	for subscriber := range r.subscribers {
		r.enqueueSubscriberLocked(subscriber, frame)
		r.removeSubscriberLocked(subscriber)
	}
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	r.manager.complete(r)
}

func (r *runState) fail(message string) {
	r.abort()
	r.publishTerminal(runStreamItem{
		name: "run_error",
		payload: runErrorEvent{
			Type:    "run_error",
			Error:   message,
			Session: harness.SessionSnapshot{SessionID: r.sessionID},
		},
	})
}

func (r *runState) abort() bool {
	r.mu.Lock()
	if r.done || r.cancel == nil {
		r.mu.Unlock()
		return false
	}
	cancel := r.cancel
	r.mu.Unlock()
	cancel()
	return true
}

func (r *runState) enqueueToSubscribersLocked(frame runFrame) {
	for subscriber := range r.subscribers {
		r.enqueueSubscriberLocked(subscriber, frame)
	}
}

func (r *runState) enqueueSubscriberLocked(subscriber *runSubscriber, frame runFrame) {
	if !subscriber.attached {
		return
	}
	if subscriber.pendingEvents+1 > r.limits.MaxSubscriberQueueEvents ||
		subscriber.pendingBytes+frame.size() > r.limits.MaxSubscriberQueueBytes ||
		r.liveQueueBytes+frame.size() > r.limits.MaxLiveQueueBytes {
		r.removeSubscriberLocked(subscriber)
		return
	}
	subscriber.pendingEvents++
	subscriber.pendingBytes += frame.size()
	r.liveQueueBytes += frame.size()
	subscriber.queue <- frame
}

func (r *runState) acknowledge(subscriber *runSubscriber, size int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !subscriber.attached {
		return
	}
	if subscriber.pendingEvents > 0 {
		subscriber.pendingEvents--
	}
	if size > subscriber.pendingBytes {
		size = subscriber.pendingBytes
	}
	subscriber.pendingBytes -= size
	if size > r.liveQueueBytes {
		r.liveQueueBytes = 0
		return
	}
	r.liveQueueBytes -= size
}

func (r *runState) removeSubscriber(subscriber *runSubscriber) {
	r.mu.Lock()
	r.removeSubscriberLocked(subscriber)
	r.mu.Unlock()
}

func (r *runState) removeSubscriberLocked(subscriber *runSubscriber) {
	if !subscriber.attached {
		return
	}
	delete(r.subscribers, subscriber)
	subscriber.attached = false
	if subscriber.pendingBytes > r.liveQueueBytes {
		r.liveQueueBytes = 0
	} else {
		r.liveQueueBytes -= subscriber.pendingBytes
	}
	subscriber.pendingBytes = 0
	subscriber.pendingEvents = 0
	subscriber.closeOnce.Do(func() { close(subscriber.queue) })
}

func encodeRunFrame(item runStreamItem) (runFrame, error) {
	data, err := json.Marshal(item.payload)
	if err != nil {
		return runFrame{}, err
	}
	frame := make([]byte, 0, len(item.name)+len(data)+16)
	frame = append(frame, "event: "...)
	frame = append(frame, item.name...)
	frame = append(frame, '\n')
	frame = append(frame, "data: "...)
	frame = append(frame, data...)
	frame = append(frame, '\n', '\n')
	return runFrame{data: frame}, nil
}

func encodeTerminalRunFrame(item runStreamItem, limits config.RunLimits) (runFrame, error) {
	item = terminalItemWithErrorLimit(item, limits.MaxErrorTextBytes)
	frame, err := encodeRunFrame(item)
	if err != nil || frame.size() <= limits.MaxTerminalBytes {
		return frame, err
	}

	for idLimit, leafLimit := 256, 256; ; {
		item = boundedTerminalItem(item, limits.MaxErrorTextBytes, idLimit, leafLimit)
		frame, err = encodeRunFrame(item)
		if err == nil && frame.size() <= limits.MaxTerminalBytes {
			return frame, nil
		}
		if idLimit == 0 && leafLimit == 0 {
			return runFrame{}, fmt.Errorf("terminal frame exceeds %d byte limit", limits.MaxTerminalBytes)
		}
		if idLimit >= leafLimit && idLimit > 0 {
			idLimit /= 2
		} else if leafLimit > 0 {
			leafLimit /= 2
		}
	}
}

func boundedTerminalItem(item runStreamItem, errorLimit, idLimit, leafLimit int) runStreamItem {
	item = terminalItemWithErrorLimit(item, errorLimit)
	switch payload := item.payload.(type) {
	case runDoneEvent:
		payload.Session = boundedRunSnapshot(payload.Session, idLimit, leafLimit)
		return runStreamItem{name: item.name, payload: payload}
	case runErrorEvent:
		payload.Session = boundedRunSnapshot(payload.Session, idLimit, leafLimit)
		return runStreamItem{name: item.name, payload: payload}
	default:
		return item
	}
}

func terminalItemWithErrorLimit(item runStreamItem, errorLimit int) runStreamItem {
	if payload, ok := item.payload.(runErrorEvent); ok {
		payload.Error = truncateRunText(payload.Error, errorLimit)
		return runStreamItem{name: item.name, payload: payload}
	}
	return item
}

func boundedRunSnapshot(snapshot harness.SessionSnapshot, idLimit, leafLimit int) harness.SessionSnapshot {
	if idLimit >= 0 {
		snapshot.SessionID = truncateRunText(snapshot.SessionID, idLimit)
	}
	if leafLimit >= 0 {
		snapshot.LeafID = truncateRunText(snapshot.LeafID, leafLimit)
	}
	snapshot.EditorText = ""
	snapshot.Messages = protocol.MessageList{}
	snapshot.Events = []protocol.AgentEvent{}
	snapshot.Tools = []protocol.ToolDefinition{}
	snapshot.Entries = []runtimesession.Entry{}
	return snapshot
}

func truncateRunText(value string, maxBytes int) string {
	if maxBytes < 0 || len(value) <= maxBytes {
		return value
	}
	if maxBytes == 0 {
		return ""
	}
	const suffix = "…"
	if maxBytes <= len(suffix) {
		return ""
	}
	end := maxBytes - len(suffix)
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end] + suffix
}
