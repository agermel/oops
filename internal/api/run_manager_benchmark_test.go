package api

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"oops/internal/config"
)

// BenchmarkRunManagerDefault32ActiveReplay records the configured worst-case
// replay-cache shape: 32 active runs, each retaining 512 bounded frames.
func BenchmarkRunManagerDefault32ActiveReplay(b *testing.B) {
	limits := config.DefaultRunLimits()
	limits.CompletedTTL = time.Hour
	item := testRunItem(0, strings.Repeat("x", 7<<10))
	frame, err := encodeRunFrame(item)
	if err != nil {
		b.Fatalf("encode benchmark frame: %v", err)
	}
	if frame.size() > limits.MaxEventBytes {
		b.Fatalf("benchmark frame size = %d, max event bytes = %d", frame.size(), limits.MaxEventBytes)
	}
	if frame.size()*limits.MaxRetainedEvents > limits.MaxRetainedBytes {
		b.Fatalf("benchmark replay size = %d, max retained bytes = %d", frame.size()*limits.MaxRetainedEvents, limits.MaxRetainedBytes)
	}

	b.SetBytes(int64(limits.MaxActiveRuns * limits.MaxRetainedEvents * frame.size()))
	b.ReportMetric(float64(limits.MaxActiveRuns*limits.MaxRetainedBytes)/(1<<20), "MiB_replay_cap")
	b.ReportAllocs()
	for iteration := 0; iteration < b.N; iteration++ {
		manager := newRunManager(limits)
		runs := make([]*runState, 0, limits.MaxActiveRuns)
		for slot := 0; slot < limits.MaxActiveRuns; slot++ {
			reservation, err := manager.reserve()
			if err != nil {
				b.Fatalf("reserve slot %d: %v", slot, err)
			}
			run, err := reservation.activate(fmt.Sprintf("benchmark-%d-%d", iteration, slot), "session", "", func() {})
			if err != nil {
				b.Fatalf("activate slot %d: %v", slot, err)
			}
			for event := 0; event < limits.MaxRetainedEvents; event++ {
				run.publish(item)
			}
			if run.done {
				b.Fatalf("run %d reached a limit before retaining %d events", slot, limits.MaxRetainedEvents)
			}
			runs = append(runs, run)
		}
		for _, run := range runs {
			run.publishTerminal(runStreamItem{name: "run_done", payload: runDoneEvent{Type: "run_done"}})
			run.finishExecution()
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if err := manager.close(ctx); err != nil {
			cancel()
			b.Fatalf("close benchmark manager: %v", err)
		}
		cancel()
	}
}
