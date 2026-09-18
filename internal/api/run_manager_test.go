package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	protocol "oops/internal/agent/ai"
	agentruntime "oops/internal/agent/runtime"
	"oops/internal/config"
)

type runManagerTestPayload struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Text  string `json:"text,omitempty"`
}

func testRunItem(index int, text string) runStreamItem {
	return runStreamItem{
		name: "agent_start",
		payload: runManagerTestPayload{
			Type:  "agent_start",
			Index: index,
			Text:  text,
		},
	}
}

func readRunSubscription(t *testing.T, subscription *runSubscription) []runFrame {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var frames []runFrame
	for {
		frame, live, ok := subscription.next(ctx)
		if !ok {
			if err := ctx.Err(); err != nil {
				t.Fatalf("subscription did not close: %v", err)
			}
			return frames
		}
		frames = append(frames, frame)
		if live {
			subscription.acknowledge(frame)
		}
	}
}

func assertTerminalSnapshotJSONArrays(t *testing.T, frame runFrame) {
	t.Helper()
	_, data, ok := strings.Cut(string(frame.data), "\ndata: ")
	if !ok {
		t.Fatal("terminal event has no data")
	}
	var event struct {
		Session json.RawMessage `json:"session"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(data)), &event); err != nil {
		t.Fatal(err)
	}
	assertSnapshotJSONArrays(t, event.Session)
}

func TestRunManagerDefaultActiveCapacity(t *testing.T) {
	manager := newRunManagerForTest(t)
	limits := config.DefaultRunLimits()
	reservations := make([]*runReservation, 0, limits.MaxActiveRuns)
	for i := 0; i < limits.MaxActiveRuns; i++ {
		reservation, err := manager.reserve()
		if err != nil {
			t.Fatalf("reserve slot %d: %v", i, err)
		}
		reservations = append(reservations, reservation)
	}
	if _, err := manager.reserve(); !errors.Is(err, ErrRunCapacity) {
		t.Fatalf("reserve beyond %d active runs = %v, want ErrRunCapacity", limits.MaxActiveRuns, err)
	}
	for _, reservation := range reservations {
		reservation.release()
	}
}

func TestRunManagerReservationBoundsActiveRuns(t *testing.T) {
	limits := config.DefaultRunLimits()
	limits.MaxActiveRuns = 1
	manager := newRunManagerForTest(t, limits)

	first, err := manager.reserve()
	if err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	if _, err := manager.reserve(); !errors.Is(err, ErrRunCapacity) {
		t.Fatalf("second reserve error = %v, want ErrRunCapacity", err)
	}
	first.release()

	second, err := manager.reserve()
	if err != nil {
		t.Fatalf("reserve after release: %v", err)
	}
	second.release()
}

func TestRunManagerSubscriberLimitAndSlowSubscriberEviction(t *testing.T) {
	limits := config.DefaultRunLimits()
	limits.MaxSubscribers = 1
	limits.MaxSubscriberQueueEvents = 1
	limits.MaxSubscriberQueueBytes = 1024
	limits.MaxLiveQueueBytes = 1024
	manager := newRunManagerForTest(t, limits)
	run := activateRunForTest(t, manager, "run-1", "sess-1")

	subscription, err := manager.subscribe(run.id)
	if err != nil {
		t.Fatalf("first subscribe: %v", err)
	}
	defer subscription.unsubscribe()
	if _, err := manager.subscribe(run.id); !errors.Is(err, ErrRunSubscriberLimit) {
		t.Fatalf("second subscribe error = %v, want ErrRunSubscriberLimit", err)
	}

	run.publish(runStreamItem{name: "agent_start", payload: protocol.AgentEvent{Type: protocol.AgentEventAgentStart}})
	run.publish(runStreamItem{name: "agent_end", payload: protocol.AgentEvent{Type: protocol.AgentEventAgentEnd}})

	first, live, ok := subscription.next(t.Context())
	if !ok || !live || !strings.Contains(string(first.data), "event: agent_start\n") {
		t.Fatalf("first live frame = %q, live=%t ok=%t", first.data, live, ok)
	}
	subscription.acknowledge(first)
	if _, _, ok := subscription.next(t.Context()); ok {
		t.Fatal("evicted subscriber still receives live frames")
	}

	run.mu.Lock()
	defer run.mu.Unlock()
	if len(run.subscribers) != 0 || run.liveQueueBytes != 0 {
		t.Fatalf("subscriber state = %d/%d, want 0/0", len(run.subscribers), run.liveQueueBytes)
	}
}

func TestRunManagerAcknowledgementKeepsRemainingQueueBudget(t *testing.T) {
	limits := config.DefaultRunLimits()
	limits.MaxSubscriberQueueEvents = 2
	limits.MaxSubscriberQueueBytes = 1024
	limits.MaxLiveQueueBytes = 1024
	manager := newRunManagerForTest(t, limits)
	run := activateRunForTest(t, manager, "run-1", "sess-1")

	subscription, err := manager.subscribe(run.id)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer subscription.unsubscribe()
	run.publish(runStreamItem{name: "agent_start", payload: protocol.AgentEvent{Type: protocol.AgentEventAgentStart}})
	run.publish(runStreamItem{name: "agent_end", payload: protocol.AgentEvent{Type: protocol.AgentEventAgentEnd}})

	first, live, ok := subscription.next(t.Context())
	if !ok || !live {
		t.Fatalf("first live frame = %q, live=%t, ok=%t", first.data, live, ok)
	}
	subscription.acknowledge(first)

	run.mu.Lock()
	defer run.mu.Unlock()
	if subscription.subscriber.pendingEvents != 1 {
		t.Fatalf("pending events = %d, want 1", subscription.subscriber.pendingEvents)
	}
	if subscription.subscriber.pendingBytes != run.history[1].size() {
		t.Fatalf("pending bytes = %d, want %d", subscription.subscriber.pendingBytes, run.history[1].size())
	}
	if run.liveQueueBytes != run.history[1].size() {
		t.Fatalf("live queue bytes = %d, want %d", run.liveQueueBytes, run.history[1].size())
	}
}

func TestRunManagerRetainsLatestHistoryWithoutCancellingRun(t *testing.T) {
	limits := config.DefaultRunLimits()
	limits.MaxRetainedEvents = 1
	limits.MaxRetainedBytes = 1024
	limits.MaxTerminalBytes = 512
	limits.MaxErrorTextBytes = 64
	manager := newRunManagerForTest(t, limits)
	run, runCtx := activateRunWithContextForTest(t, manager, "run-1", "sess-1")
	subscription, err := manager.subscribe(run.id)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer subscription.unsubscribe()

	run.publish(runStreamItem{name: "agent_start", payload: protocol.AgentEvent{Type: protocol.AgentEventAgentStart}})
	run.publish(runStreamItem{name: "agent_end", payload: protocol.AgentEvent{Type: protocol.AgentEventAgentEnd}})

	if err := runCtx.Err(); err != nil {
		t.Fatalf("run context after history eviction = %v, want active", err)
	}
	first, live, ok := subscription.next(t.Context())
	if !ok || !live || first.sequence != 1 || !strings.Contains(string(first.data), "event: agent_start\n") {
		t.Fatalf("first live frame = %q, sequence=%d live=%t ok=%t", first.data, first.sequence, live, ok)
	}
	subscription.acknowledge(first)
	second, live, ok := subscription.next(t.Context())
	if !ok || !live || second.sequence != 2 || !strings.Contains(string(second.data), "event: agent_end\n") {
		t.Fatalf("second live frame = %q, sequence=%d live=%t ok=%t", second.data, second.sequence, live, ok)
	}
	subscription.acknowledge(second)

	run.mu.Lock()
	done := run.done
	normalEvents := run.normalEvents
	history := append([]runFrame(nil), run.history...)
	run.mu.Unlock()
	if done || normalEvents != 1 || len(history) != 1 {
		t.Fatalf("retained state = done:%t events:%d history:%d, want false/1/1", done, normalEvents, len(history))
	}
	if history[0].sequence != 2 {
		t.Fatalf("retained sequence = %d, want 2", history[0].sequence)
	}

	run.publishTerminal(runStreamItem{
		name: "run_done",
		payload: runDoneEvent{
			Type:    "run_done",
			Session: agentruntime.SessionSnapshot{SessionID: "sess-1"},
		},
	})
	terminal, live, ok := subscription.next(t.Context())
	if !ok || !live || terminal.sequence != 3 || !strings.Contains(string(terminal.data), "event: run_done\n") {
		t.Fatalf("terminal live frame = %q, sequence=%d live=%t ok=%t", terminal.data, terminal.sequence, live, ok)
	}
	run.publish(testRunItem(3, "ignored after terminal"))
	run.publishTerminal(runStreamItem{
		name: "run_error",
		payload: runErrorEvent{
			Type:  "run_error",
			Error: "ignored duplicate terminal",
		},
	})

	reconnected, err := manager.subscribe(run.id)
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	frames := readRunSubscription(t, reconnected)
	if len(frames) != 2 || frames[0].sequence != 2 || frames[1].sequence != 3 {
		t.Fatalf("replayed frames = %#v, want sequences 2 and 3", frames)
	}
	if !strings.Contains(string(frames[0].data), "event: agent_end\n") ||
		!strings.Contains(string(frames[1].data), `"sessionId":"sess-1"`) {
		t.Fatalf("replayed tail = %q", frames)
	}
	dataPrefix := []byte("data: ")
	dataStart := strings.Index(string(frames[1].data), string(dataPrefix))
	if dataStart < 0 {
		t.Fatalf("terminal frame has no data field: %q", frames[1].data)
	}
	var doneEvent runDoneEvent
	data := frames[1].data[dataStart+len(dataPrefix):]
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &doneEvent); err != nil {
		t.Fatalf("decode terminal payload: %v", err)
	}
	if doneEvent.Type != "run_done" || doneEvent.Session.SessionID != "sess-1" {
		t.Fatalf("terminal payload = %#v", doneEvent)
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	if run.nextSequence != 3 || run.normalEvents != 1 || len(run.history) != 2 {
		t.Fatalf("terminal state = sequence:%d normal:%d history:%d, want 3/1/2",
			run.nextSequence, run.normalEvents, len(run.history))
	}
}

func TestRunManagerLargeEventsAndRetainedByteLimits(t *testing.T) {
	item := testRunItem(1, strings.Repeat("x", 128))
	frame, err := encodeRunFrame(item)
	if err != nil {
		t.Fatalf("encode test frame: %v", err)
	}

	t.Run("large event is delivered intact and replayed", func(t *testing.T) {
		manager := newRunManagerForTest(t)
		run, runCtx := activateRunWithContextForTest(t, manager, "run-large-event", "sess-1")
		subscription, err := manager.subscribe(run.id)
		if err != nil {
			t.Fatalf("subscribe: %v", err)
		}
		defer subscription.unsubscribe()
		text := strings.Repeat("x", 300<<10)
		largeItem := testRunItem(1, text)
		want, err := encodeRunFrame(largeItem)
		if err != nil {
			t.Fatalf("encode large event: %v", err)
		}
		run.publish(largeItem)
		if err := runCtx.Err(); err != nil {
			t.Fatalf("run context after large event = %v, want active", err)
		}
		run.publishTerminal(runStreamItem{name: "run_done", payload: runDoneEvent{Type: "run_done"}})
		frames := readRunSubscription(t, subscription)
		if len(frames) != 2 || string(frames[0].data) != string(want.data) ||
			!strings.Contains(string(frames[1].data), "event: run_done\n") {
			t.Fatal("live stream did not deliver the complete large event followed by run_done")
		}
		reconnected, err := manager.subscribe(run.id)
		if err != nil {
			t.Fatalf("reconnect: %v", err)
		}
		defer reconnected.unsubscribe()
		replayed := readRunSubscription(t, reconnected)
		if len(replayed) != 2 || string(replayed[0].data) != string(want.data) ||
			!strings.Contains(string(replayed[1].data), "event: run_done\n") {
			t.Fatal("replay did not deliver the complete large event followed by run_done")
		}
	})

	t.Run("encoding failure", func(t *testing.T) {
		manager := newRunManagerForTest(t)
		run, runCtx := activateRunWithContextForTest(t, manager, "run-encoding-failure", "sess-1")

		run.publish(runStreamItem{name: "invalid", payload: func() {}})
		if runCtx.Err() == nil {
			t.Fatal("run context remains active after event encoding failure")
		}
		subscription, err := manager.subscribe(run.id)
		if err != nil {
			t.Fatalf("subscribe: %v", err)
		}
		frames := readRunSubscription(t, subscription)
		if len(frames) != 1 || !strings.Contains(string(frames[0].data), "event: run_error\n") ||
			!strings.Contains(string(frames[0].data), "encode run event") {
			t.Fatalf("frames = %q, want encoding run_error", frames)
		}
		assertTerminalSnapshotJSONArrays(t, frames[0])
	})

	t.Run("retained bytes", func(t *testing.T) {
		limits := config.DefaultRunLimits()
		limits.MaxRetainedBytes = frame.size()
		manager := newRunManagerForTest(t, limits)
		run, runCtx := activateRunWithContextForTest(t, manager, "run-byte-limit", "sess-1")
		subscription, err := manager.subscribe(run.id)
		if err != nil {
			t.Fatalf("subscribe: %v", err)
		}

		run.publish(item)
		run.publish(testRunItem(2, strings.Repeat("x", 128)))
		if err := runCtx.Err(); err != nil {
			t.Fatalf("run context after retained byte eviction = %v, want active", err)
		}

		run.mu.Lock()
		history := append([]runFrame(nil), run.history...)
		normalEvents := run.normalEvents
		normalBytes := run.normalBytes
		run.mu.Unlock()
		if len(history) != 1 || normalEvents != 1 || normalBytes != frame.size() {
			t.Fatalf("retained byte state = history:%d events:%d bytes:%d", len(history), normalEvents, normalBytes)
		}
		if history[0].sequence != 2 {
			t.Fatalf("retained sequence = %d, want 2", history[0].sequence)
		}

		run.publishTerminal(runStreamItem{name: "run_done", payload: runDoneEvent{Type: "run_done"}})
		frames := readRunSubscription(t, subscription)
		if len(frames) != 3 || !strings.Contains(string(frames[2].data), "event: run_done\n") {
			t.Fatalf("live frames = %q, want two normal frames and run_done", frames)
		}

		reconnected, err := manager.subscribe(run.id)
		if err != nil {
			t.Fatalf("reconnect: %v", err)
		}
		replayed := readRunSubscription(t, reconnected)
		if len(replayed) != 2 || replayed[0].sequence != 2 || replayed[1].sequence != 3 {
			t.Fatalf("replayed frames = %#v, want sequences 2 and 3", replayed)
		}
	})

	t.Run("retained byte eviction drops enough frames", func(t *testing.T) {
		smallItem := testRunItem(1, "tiny")
		smallFrame, err := encodeRunFrame(smallItem)
		if err != nil {
			t.Fatalf("encode small frame: %v", err)
		}
		largeItem := testRunItem(4, strings.Repeat("x", smallFrame.size()))
		largeFrame, err := encodeRunFrame(largeItem)
		if err != nil {
			t.Fatalf("encode large frame: %v", err)
		}
		budget := smallFrame.size() * 3
		if largeFrame.size() <= smallFrame.size() || largeFrame.size() > smallFrame.size()*2 || largeFrame.size() > budget {
			t.Fatalf("test frame sizes = small:%d large:%d budget:%d", smallFrame.size(), largeFrame.size(), budget)
		}
		limits := config.DefaultRunLimits()
		limits.MaxRetainedBytes = budget
		manager := newRunManagerForTest(t, limits)
		run, runCtx := activateRunWithContextForTest(t, manager, "run-multi-byte-eviction", "sess-1")

		for index := 1; index <= 3; index++ {
			run.publish(testRunItem(index, "tiny"))
		}
		run.publish(largeItem)
		if err := runCtx.Err(); err != nil {
			t.Fatalf("run context after multi-frame eviction = %v, want active", err)
		}

		run.mu.Lock()
		defer run.mu.Unlock()
		if len(run.history) != 2 || run.normalEvents != 2 {
			t.Fatalf("retained events = history:%d accounting:%d, want 2/2", len(run.history), run.normalEvents)
		}
		if run.history[0].sequence != 3 || run.history[1].sequence != 4 {
			t.Fatalf("retained sequences = %d/%d, want 3/4", run.history[0].sequence, run.history[1].sequence)
		}
		wantBytes := smallFrame.size() + largeFrame.size()
		if run.normalBytes != wantBytes || run.normalBytes > budget {
			t.Fatalf("retained bytes = %d, want %d within %d", run.normalBytes, wantBytes, budget)
		}
	})

	t.Run("frame larger than retained budget stays live", func(t *testing.T) {
		smallItem := testRunItem(0, "")
		smallFrame, err := encodeRunFrame(smallItem)
		if err != nil {
			t.Fatalf("encode small frame: %v", err)
		}
		limits := config.DefaultRunLimits()
		limits.MaxRetainedBytes = smallFrame.size()
		manager := newRunManagerForTest(t, limits)
		run, runCtx := activateRunWithContextForTest(t, manager, "run-live-only", "sess-1")
		subscription, err := manager.subscribe(run.id)
		if err != nil {
			t.Fatalf("subscribe: %v", err)
		}

		run.publish(smallItem)
		run.publish(item)
		if err := runCtx.Err(); err != nil {
			t.Fatalf("run context after live-only frame = %v, want active", err)
		}
		run.mu.Lock()
		if len(run.history) != 1 || run.normalEvents != 1 || run.normalBytes != smallFrame.size() {
			run.mu.Unlock()
			t.Fatalf("live-only retained state = history:%d events:%d bytes:%d, want 1/1/%d",
				len(run.history), run.normalEvents, run.normalBytes, smallFrame.size())
		}
		if run.history[0].sequence != 1 {
			run.mu.Unlock()
			t.Fatalf("retained sequence = %d, want 1", run.history[0].sequence)
		}
		run.mu.Unlock()

		liveFrame, live, ok := subscription.next(t.Context())
		if !ok || !live || liveFrame.sequence != 1 || !strings.Contains(string(liveFrame.data), `"index":0`) {
			t.Fatalf("live-only frame = %q, sequence=%d live=%t ok=%t", liveFrame.data, liveFrame.sequence, live, ok)
		}
		subscription.acknowledge(liveFrame)
		liveFrame, live, ok = subscription.next(t.Context())
		if !ok || !live || liveFrame.sequence != 2 || !strings.Contains(string(liveFrame.data), `"index":1`) {
			t.Fatalf("oversized retained frame = %q, sequence=%d live=%t ok=%t", liveFrame.data, liveFrame.sequence, live, ok)
		}
		subscription.acknowledge(liveFrame)
		run.publishTerminal(runStreamItem{name: "run_done", payload: runDoneEvent{Type: "run_done"}})
		terminal, live, ok := subscription.next(t.Context())
		if !ok || !live || terminal.sequence != 3 || !strings.Contains(string(terminal.data), "event: run_done\n") {
			t.Fatalf("terminal frame = %q, sequence=%d live=%t ok=%t", terminal.data, terminal.sequence, live, ok)
		}

		reconnected, err := manager.subscribe(run.id)
		if err != nil {
			t.Fatalf("reconnect: %v", err)
		}
		replayed := readRunSubscription(t, reconnected)
		if len(replayed) != 2 || replayed[0].sequence != 1 || replayed[1].sequence != 3 ||
			!strings.Contains(string(replayed[0].data), `"index":0`) ||
			!strings.Contains(string(replayed[1].data), "event: run_done\n") {
			t.Fatalf("replayed frames = %#v, want retained sequence 1 and terminal sequence 3", replayed)
		}
	})
}

func TestRunManagerRepeatedHistoryEvictionKeepsTailAccounting(t *testing.T) {
	limits := config.DefaultRunLimits()
	limits.MaxRetainedEvents = 3
	limits.MaxRetainedBytes = 4096
	manager := newRunManagerForTest(t, limits)
	run, runCtx := activateRunWithContextForTest(t, manager, "run-repeated-eviction", "sess-1")

	const published = 30
	for index := 1; index <= published; index++ {
		var previousHistory []runFrame
		if index > limits.MaxRetainedEvents {
			run.mu.Lock()
			previousHistory = run.history
			run.mu.Unlock()
		}
		run.publish(testRunItem(index, "tail"))
		if previousHistory != nil && (previousHistory[0].sequence != 0 || previousHistory[0].data != nil) {
			t.Fatalf("evicted history slot %d retained its payload", index-limits.MaxRetainedEvents)
		}

		run.mu.Lock()
		wantEvents := index
		if wantEvents > limits.MaxRetainedEvents {
			wantEvents = limits.MaxRetainedEvents
		}
		if len(run.history) != wantEvents || run.normalEvents != wantEvents {
			run.mu.Unlock()
			t.Fatalf("publish %d retained events = history:%d accounting:%d, want %d",
				index, len(run.history), run.normalEvents, wantEvents)
		}
		wantSequence := uint64(index - wantEvents + 1)
		retainedBytes := 0
		for retainedIndex, retained := range run.history {
			if retained.sequence != wantSequence+uint64(retainedIndex) {
				run.mu.Unlock()
				t.Fatalf("publish %d history sequence[%d] = %d, want %d",
					index, retainedIndex, retained.sequence, wantSequence+uint64(retainedIndex))
			}
			retainedBytes += retained.size()
		}
		if run.normalBytes != retainedBytes || retainedBytes > limits.MaxRetainedBytes {
			run.mu.Unlock()
			t.Fatalf("publish %d retained bytes = accounting:%d actual:%d limit:%d",
				index, run.normalBytes, retainedBytes, limits.MaxRetainedBytes)
		}
		run.mu.Unlock()
	}
	if err := runCtx.Err(); err != nil {
		t.Fatalf("run context after repeated eviction = %v, want active", err)
	}
}

func TestRunManagerSlowSubscriberDoesNotBlockHealthySubscriber(t *testing.T) {
	item := testRunItem(1, "live")
	frame, err := encodeRunFrame(item)
	if err != nil {
		t.Fatalf("encode test frame: %v", err)
	}
	limits := config.DefaultRunLimits()
	limits.MaxRetainedEvents = 1
	limits.MaxSubscriberQueueEvents = 1
	limits.MaxSubscriberQueueBytes = 1024
	limits.MaxLiveQueueBytes = frame.size() * 2
	manager := newRunManagerForTest(t, limits)
	run, runCtx := activateRunWithContextForTest(t, manager, "run-subscriber-isolation", "sess-1")

	slow, err := manager.subscribe(run.id)
	if err != nil {
		t.Fatalf("subscribe slow client: %v", err)
	}
	defer slow.unsubscribe()
	healthy, err := manager.subscribe(run.id)
	if err != nil {
		t.Fatalf("subscribe healthy client: %v", err)
	}
	defer healthy.unsubscribe()

	run.publish(item)
	first, live, ok := healthy.next(t.Context())
	if !ok || !live || first.sequence != 1 {
		t.Fatalf("healthy first frame = %#v, live=%t ok=%t", first, live, ok)
	}
	healthy.acknowledge(first)

	run.publish(testRunItem(2, "live"))
	second, live, ok := healthy.next(t.Context())
	if !ok || !live || second.sequence != 2 {
		t.Fatalf("healthy second frame = %#v, live=%t ok=%t", second, live, ok)
	}
	healthy.acknowledge(second)
	if err := runCtx.Err(); err != nil {
		t.Fatalf("run context after slow subscriber eviction = %v, want active", err)
	}
	run.mu.Lock()
	_, slowRegistered := run.subscribers[slow.subscriber]
	slowAttached := slow.subscriber.attached
	liveQueueBytes := run.liveQueueBytes
	run.mu.Unlock()
	if slowRegistered || slowAttached || liveQueueBytes != 0 {
		t.Fatalf("state before terminal = slow registered:%t attached:%t live bytes:%d, want false/false/0",
			slowRegistered, slowAttached, liveQueueBytes)
	}

	run.publishTerminal(runStreamItem{name: "run_done", payload: runDoneEvent{Type: "run_done"}})
	terminal, live, ok := healthy.next(t.Context())
	if !ok || !live || terminal.sequence != 3 || !strings.Contains(string(terminal.data), "event: run_done\n") {
		t.Fatalf("healthy terminal = %q, sequence=%d live=%t ok=%t", terminal.data, terminal.sequence, live, ok)
	}
	if _, _, ok := healthy.next(t.Context()); ok {
		t.Fatal("healthy subscription remained open after terminal")
	}
	slowFrames := readRunSubscription(t, slow)
	if len(slowFrames) != 1 || slowFrames[0].sequence != 1 {
		t.Fatalf("slow subscriber frames = %#v, want only sequence 1", slowFrames)
	}

	run.mu.Lock()
	defer run.mu.Unlock()
	if len(run.subscribers) != 0 || run.liveQueueBytes != 0 {
		t.Fatalf("subscriber state = %d/%d, want 0/0", len(run.subscribers), run.liveQueueBytes)
	}
}

func TestRunManagerLiveByteLimitEvictsSlowSubscriberAndReconnectsTerminal(t *testing.T) {
	item := testRunItem(1, strings.Repeat("x", 64))
	frame, err := encodeRunFrame(item)
	if err != nil {
		t.Fatalf("encode test frame: %v", err)
	}
	limits := config.DefaultRunLimits()
	limits.MaxSubscriberQueueEvents = 64
	limits.MaxSubscriberQueueBytes = frame.size() * 4
	limits.MaxLiveQueueBytes = frame.size()
	manager := newRunManagerForTest(t, limits)
	run := activateRunForTest(t, manager, "run-slow-subscriber", "sess-1")

	slow, err := manager.subscribe(run.id)
	if err != nil {
		t.Fatalf("subscribe slow client: %v", err)
	}
	run.publish(item)
	run.publish(testRunItem(2, strings.Repeat("x", 64)))
	run.publishTerminal(runStreamItem{name: "run_done", payload: runDoneEvent{Type: "run_done"}})
	run.finishExecution()

	slowFrames := readRunSubscription(t, slow)
	if len(slowFrames) != 1 || slowFrames[0].sequence != 1 {
		t.Fatalf("slow subscriber frames = %#v, want only first live frame", slowFrames)
	}
	run.mu.Lock()
	if len(run.subscribers) != 0 || run.liveQueueBytes != 0 {
		run.mu.Unlock()
		t.Fatalf("slow subscriber state = %d/%d, want 0/0", len(run.subscribers), run.liveQueueBytes)
	}
	run.mu.Unlock()

	reconnected, err := manager.subscribe(run.id)
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	frames := readRunSubscription(t, reconnected)
	if len(frames) != 3 {
		t.Fatalf("reconnected frame count = %d, want 3", len(frames))
	}
	for index, replayed := range frames {
		if replayed.sequence != uint64(index+1) {
			t.Fatalf("reconnected sequence[%d] = %d, want %d", index, replayed.sequence, index+1)
		}
	}
	if !strings.Contains(string(frames[2].data), "event: run_done\n") {
		t.Fatalf("reconnected terminal = %q, want run_done", frames[2].data)
	}
}

func TestRunManagerReplaysDefaultHistoryCapacityInStrictSequence(t *testing.T) {
	limits := config.DefaultRunLimits()
	limits.CompletedTTL = time.Hour
	manager := newRunManagerForTest(t, limits)
	run := activateRunForTest(t, manager, "run-history-capacity", "sess-1")

	for index := 0; index < limits.MaxRetainedEvents; index++ {
		run.publish(testRunItem(index, "history"))
	}
	run.publishTerminal(runStreamItem{name: "run_done", payload: runDoneEvent{Type: "run_done"}})
	run.finishExecution()

	subscription, err := manager.subscribe(run.id)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	frames := readRunSubscription(t, subscription)
	if len(frames) != limits.MaxRetainedEvents+1 {
		t.Fatalf("replayed frame count = %d, want %d", len(frames), limits.MaxRetainedEvents+1)
	}
	for index, frame := range frames {
		if frame.sequence != uint64(index+1) {
			t.Fatalf("sequence[%d] = %d, want %d", index, frame.sequence, index+1)
		}
	}
	if !strings.Contains(string(frames[len(frames)-1].data), "event: run_done\n") {
		t.Fatalf("last replayed frame = %q, want run_done", frames[len(frames)-1].data)
	}
}

func TestRunManagerConcurrentSubscribeAndPublishKeepsOrderedTerminal(t *testing.T) {
	limits := config.DefaultRunLimits()
	limits.CompletedTTL = time.Hour
	limits.MaxRetainedEvents = 1
	manager := newRunManagerForTest(t, limits)

	const rounds = 64
	for round := 0; round < rounds; round++ {
		run := activateRunForTest(t, manager, fmt.Sprintf("run-linearized-%d", round), "sess-1")
		start := make(chan struct{})
		subscriptions := make(chan *runSubscription, 1)
		errs := make(chan error, 1)
		var workers sync.WaitGroup
		workers.Add(2)
		go func() {
			defer workers.Done()
			<-start
			subscription, err := manager.subscribe(run.id)
			if err != nil {
				errs <- err
				return
			}
			subscriptions <- subscription
		}()
		go func() {
			defer workers.Done()
			<-start
			run.publish(testRunItem(1, "first"))
			run.publish(testRunItem(2, "second"))
			run.publishTerminal(runStreamItem{name: "run_done", payload: runDoneEvent{Type: "run_done"}})
			run.finishExecution()
		}()
		close(start)
		workers.Wait()
		select {
		case err := <-errs:
			t.Fatalf("round %d subscribe: %v", round, err)
		default:
		}
		subscription := <-subscriptions
		frames := readRunSubscription(t, subscription)
		if len(frames) < 2 || len(frames) > 3 {
			t.Fatalf("round %d frame count = %d, want an ordered suffix of length 2 or 3", round, len(frames))
		}
		wantFirstSequence := uint64(4 - len(frames))
		if frames[0].sequence != wantFirstSequence {
			t.Fatalf("round %d first sequence = %d, want %d", round, frames[0].sequence, wantFirstSequence)
		}
		terminalCount := 0
		for index, frame := range frames {
			wantSequence := wantFirstSequence + uint64(index)
			if frame.sequence != wantSequence {
				t.Fatalf("round %d sequence[%d] = %d, want %d", round, index, frame.sequence, wantSequence)
			}
			if strings.Contains(string(frame.data), "event: run_done\n") {
				terminalCount++
				if index != len(frames)-1 {
					t.Fatalf("round %d terminal appeared at frame %d of %d", round, index, len(frames))
				}
			}
		}
		if terminalCount != 1 || frames[len(frames)-1].sequence != 3 {
			t.Fatalf("round %d terminal count/sequence = %d/%d, want 1/3",
				round, terminalCount, frames[len(frames)-1].sequence)
		}
	}
}

func TestRunManagerAbortAndTerminalRaceKeepsOneTerminal(t *testing.T) {
	manager := newRunManagerForTest(t)

	const rounds = 64
	for round := 0; round < rounds; round++ {
		run := activateRunForTest(t, manager, fmt.Sprintf("run-terminal-race-%d", round), "sess-1")
		start := make(chan struct{})
		var workers sync.WaitGroup
		workers.Add(2)
		go func() {
			defer workers.Done()
			<-start
			run.abort()
		}()
		go func() {
			defer workers.Done()
			<-start
			run.publishTerminal(runStreamItem{name: "run_done", payload: runDoneEvent{Type: "run_done"}})
		}()
		close(start)
		workers.Wait()
		run.finishExecution()

		run.mu.Lock()
		terminalCount := 0
		for _, frame := range run.history {
			if strings.Contains(string(frame.data), "event: run_done\n") || strings.Contains(string(frame.data), "event: run_error\n") {
				terminalCount++
			}
		}
		run.mu.Unlock()
		if terminalCount != 1 {
			t.Fatalf("round %d terminal count = %d, want 1", round, terminalCount)
		}
	}
}

func TestRunManagerPublishAndTerminalRaceKeepsBoundedOrderedHistory(t *testing.T) {
	limits := config.DefaultRunLimits()
	limits.CompletedTTL = time.Hour
	limits.MaxRetainedEvents = 2
	manager := newRunManagerForTest(t, limits)

	const (
		rounds     = 64
		publishers = 8
	)
	for round := 0; round < rounds; round++ {
		run := activateRunForTest(t, manager, fmt.Sprintf("run-publish-terminal-race-%d", round), "sess-1")
		for seed := 0; seed <= limits.MaxRetainedEvents; seed++ {
			run.publish(testRunItem(seed, "seed"))
		}
		start := make(chan struct{})
		var workers sync.WaitGroup
		workers.Add(publishers + 2)
		for publisher := 0; publisher < publishers; publisher++ {
			go func(index int) {
				defer workers.Done()
				<-start
				run.publish(testRunItem(index, "race"))
			}(publisher)
		}
		for terminal := 0; terminal < 2; terminal++ {
			go func() {
				defer workers.Done()
				<-start
				run.publishTerminal(runStreamItem{name: "run_done", payload: runDoneEvent{Type: "run_done"}})
			}()
		}
		close(start)
		workers.Wait()
		run.finishExecution()

		run.mu.Lock()
		history := append([]runFrame(nil), run.history...)
		normalEvents := run.normalEvents
		normalBytes := run.normalBytes
		nextSequence := run.nextSequence
		done := run.done
		run.mu.Unlock()
		if !done || normalEvents != limits.MaxRetainedEvents || len(history) != normalEvents+1 {
			t.Fatalf("round %d state = done:%t history:%d normal:%d", round, done, len(history), normalEvents)
		}
		terminalCount := 0
		retainedBytes := 0
		for index, frame := range history {
			if index > 0 && frame.sequence <= history[index-1].sequence {
				t.Fatalf("round %d sequence[%d] = %d after %d", round, index, frame.sequence, history[index-1].sequence)
			}
			if strings.Contains(string(frame.data), "event: run_done\n") {
				terminalCount++
				if index != len(history)-1 {
					t.Fatalf("round %d terminal appeared before history tail", round)
				}
				continue
			}
			retainedBytes += frame.size()
		}
		if terminalCount != 1 || history[len(history)-1].sequence != nextSequence {
			t.Fatalf("round %d terminal count/sequence = %d/%d, next=%d",
				round, terminalCount, history[len(history)-1].sequence, nextSequence)
		}
		if retainedBytes != normalBytes || normalBytes > limits.MaxRetainedBytes {
			t.Fatalf("round %d retained bytes = actual:%d accounting:%d limit:%d",
				round, retainedBytes, normalBytes, limits.MaxRetainedBytes)
		}

		run.publish(testRunItem(publishers, "ignored"))
		run.publishTerminal(runStreamItem{name: "run_error", payload: runErrorEvent{Type: "run_error", Error: "ignored"}})
		run.mu.Lock()
		if len(run.history) != len(history) || run.nextSequence != nextSequence {
			run.mu.Unlock()
			t.Fatalf("round %d done state changed after later publish", round)
		}
		run.mu.Unlock()
	}
}

func TestRunManagerReleasesCapacityAfterExecutionFinishes(t *testing.T) {
	limits := config.DefaultRunLimits()
	limits.MaxActiveRuns = 1
	manager := newRunManagerForTest(t, limits)
	reservation, err := manager.reserve()
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	run, err := reservation.activate("run-1", "sess-1", "", func() {})
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	run.publishTerminal(runStreamItem{name: "run_done", payload: runDoneEvent{Type: "run_done"}})
	if _, err := manager.reserve(); !errors.Is(err, ErrRunCapacity) {
		t.Fatalf("reserve while execution is still unwinding = %v, want ErrRunCapacity", err)
	}
	run.finishExecution()
	second, err := manager.reserve()
	if err != nil {
		t.Fatalf("reserve after execution finished: %v", err)
	}
	second.release()
}

func TestRuntimeAllowsOneWriterPerSessionUntilRelease(t *testing.T) {
	runtime := agentruntime.NewRuntime(agentruntime.RuntimeOptions{})
	first, err := runtime.AcquireSession("sess-1")
	if err != nil {
		t.Fatalf("claim first session: %v", err)
	}
	if second, err := runtime.AcquireSession("sess-1"); !errors.Is(err, ErrSessionBusy) {
		if second != nil {
			second.Release()
		}
		t.Fatalf("claim session during active lease = %v, want ErrSessionBusy", err)
	}
	first.Release()
	third, err := runtime.AcquireSession("sess-1")
	if err != nil {
		t.Fatalf("claim session after release: %v", err)
	}
	third.Release()
}

func TestRunManagerBoundsTerminalSnapshotAndExpiresCompletedRun(t *testing.T) {
	limits := config.DefaultRunLimits()
	limits.MaxTerminalBytes = 512
	limits.MaxErrorTextBytes = 32
	limits.CompletedTTL = 10 * time.Millisecond
	manager := newRunManagerForTest(t, limits)
	run := activateRunForTest(t, manager, "run-1", strings.Repeat("s", 128))

	run.publishTerminal(runStreamItem{
		name: "run_done",
		payload: runDoneEvent{
			Type: "run_done",
			Session: agentruntime.SessionSnapshot{
				SessionID: strings.Repeat("s", 128),
				LeafID:    strings.Repeat("l", 128),
				Messages:  protocol.MessageList{protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent(strings.Repeat("x", 2048))}}},
			},
		},
	})
	run.finishExecution()

	subscription, err := manager.subscribe(run.id)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	frame, _, ok := subscription.next(t.Context())
	if !ok {
		t.Fatal("terminal frame missing")
	}
	if frame.size() > limits.MaxTerminalBytes {
		t.Fatalf("terminal frame size = %d, limit = %d", frame.size(), limits.MaxTerminalBytes)
	}
	if !strings.Contains(string(frame.data), `"messages":[]`) {
		t.Fatalf("terminal frame did not use bounded snapshot: %q", frame.data)
	}

	deadline := time.Now().Add(time.Second)
	for {
		if _, exists := manager.get(run.id); !exists {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("completed run did not expire")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRunManagerTerminalSnapshotJSONArrays(t *testing.T) {
	for _, eventType := range []string{"run_done", "run_error"} {
		for _, scenario := range []string{"empty", "bounded", "invalid"} {
			t.Run(eventType+"/"+scenario, func(t *testing.T) {
				manager := newRunManagerForTest(t)
				run := activateRunForTest(t, manager, "run-terminal-arrays", "s1")
				snapshot := agentruntime.SessionSnapshot{SessionID: "s1"}
				if scenario == "bounded" {
					snapshot.Messages = protocol.MessageList{protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent(strings.Repeat("x", manager.limits.MaxTerminalBytes))}}}
				} else if scenario == "invalid" {
					snapshot.Messages = protocol.MessageList{nil}
				}
				item := runStreamItem{name: eventType, payload: runDoneEvent{Type: eventType, Session: snapshot}}
				if eventType == "run_error" {
					item.payload = runErrorEvent{Type: eventType, Error: "original failure", Session: snapshot}
				}
				run.publishTerminal(item)
				subscription, err := manager.subscribe(run.id)
				if err != nil {
					t.Fatal(err)
				}
				frames := readRunSubscription(t, subscription)
				if len(frames) != 1 {
					t.Fatalf("terminal count = %d, want 1", len(frames))
				}
				assertTerminalSnapshotJSONArrays(t, frames[0])
				want := "event: " + eventType + "\n"
				if scenario == "invalid" {
					want = "run terminal event could not be encoded"
				} else if eventType == "run_error" {
					want = "original failure"
				}
				if !strings.Contains(string(frames[0].data), want) || frames[0].size() > manager.limits.MaxTerminalBytes {
					t.Fatal("terminal event lost its outcome or exceeded the byte budget")
				}
			})
		}
	}
}

func TestRunManagerClampsTerminalBudgetToEncodableFrame(t *testing.T) {
	limits := config.DefaultRunLimits()
	limits.MaxTerminalBytes = 1
	manager := newRunManagerForTest(t, limits)
	if manager.limits.MaxTerminalBytes < 256 {
		t.Fatalf("terminal budget = %d, want at least 256", manager.limits.MaxTerminalBytes)
	}
	run := activateRunForTest(t, manager, "run-1", strings.Repeat("s", 4096))
	run.publishTerminal(runStreamItem{
		name: "run_done",
		payload: runDoneEvent{
			Type: "run_done",
			Session: agentruntime.SessionSnapshot{
				SessionID: strings.Repeat("s", 4096),
				LeafID:    strings.Repeat("l", 4096),
			},
		},
	})

	subscription, err := manager.subscribe(run.id)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	frame, _, ok := subscription.next(t.Context())
	if !ok {
		t.Fatal("terminal frame missing")
	}
	if frame.size() > manager.limits.MaxTerminalBytes {
		t.Fatalf("terminal frame size = %d, limit = %d", frame.size(), manager.limits.MaxTerminalBytes)
	}
}

func TestRunManagerCloseWaitsForExecution(t *testing.T) {
	manager := newRunManager()
	reservation, err := manager.reserve()
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	ctx, cancelRun := context.WithCancel(context.Background())
	run, err := reservation.activate("run-1", "sess-1", "", cancelRun)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}

	closeCtx, cancelClose := context.WithTimeout(context.Background(), time.Second)
	defer cancelClose()
	closed := make(chan error, 1)
	go func() { closed <- manager.close(closeCtx) }()
	select {
	case err := <-closed:
		t.Fatalf("close returned before execution ended: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if ctx.Err() == nil {
		t.Fatal("close did not cancel active run")
	}

	run.publishTerminal(runStreamItem{name: "run_done", payload: runDoneEvent{Type: "run_done"}})
	run.finishExecution()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("close did not wait for execution completion")
	}
}

func TestRunManagerCloseWaitsForReservationRelease(t *testing.T) {
	manager := newRunManager()
	reservation, err := manager.reserve()
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}

	closeCtx, cancelClose := context.WithTimeout(context.Background(), time.Second)
	defer cancelClose()
	closed := make(chan error, 1)
	go func() { closed <- manager.close(closeCtx) }()
	select {
	case err := <-closed:
		t.Fatalf("close returned before reservation release: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	reservation.release()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("close did not wait for reservation release")
	}
}
