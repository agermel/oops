package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"oops/internal/console"
	"oops/internal/loghub"
	"oops/internal/mcp"
	"oops/internal/nodelet"
	runtimestore "oops/internal/store/runtime"

	"go.uber.org/goleak"
)

type lifecycleResponseWriter struct {
	header    http.Header
	flushed   chan struct{}
	flushOnce sync.Once
}

func newLifecycleResponseWriter() *lifecycleResponseWriter {
	return &lifecycleResponseWriter{
		header:  make(http.Header),
		flushed: make(chan struct{}),
	}
}

func (w *lifecycleResponseWriter) Header() http.Header { return w.header }

func (w *lifecycleResponseWriter) Write(data []byte) (int, error) { return len(data), nil }

func (w *lifecycleResponseWriter) WriteHeader(int) {}

func (w *lifecycleResponseWriter) Flush() {
	w.flushOnce.Do(func() { close(w.flushed) })
}

type blockingReadCloser struct {
	readStarted chan struct{}
	closed      chan struct{}
	readOnce    sync.Once
	closeOnce   sync.Once
}

func newBlockingReadCloser() *blockingReadCloser {
	return &blockingReadCloser{
		readStarted: make(chan struct{}),
		closed:      make(chan struct{}),
	}
}

func (r *blockingReadCloser) Read([]byte) (int, error) {
	r.readOnce.Do(func() { close(r.readStarted) })
	<-r.closed
	return 0, io.EOF
}

func (r *blockingReadCloser) Close() error {
	r.closeOnce.Do(func() { close(r.closed) })
	return nil
}

type blockingStreamNodeletClient struct {
	fakeNodeletClient
	stream *blockingReadCloser
}

func (c *blockingStreamNodeletClient) ContainerLogsStream(context.Context, string, string, string, string) (io.ReadCloser, error) {
	return c.stream, nil
}

func waitForLifecycleSignal(t *testing.T, signal <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}

func closeServerForTest(t *testing.T, server *Server) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Close(ctx); err != nil {
		t.Fatalf("server close: %v", err)
	}
}

func TestServerQuiesceTerminatesRunAndConsoleSSE(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	server := New(Options{})
	reservation, err := server.runManager.reserve()
	if err != nil {
		t.Fatalf("reserve run: %v", err)
	}
	run, err := reservation.activate("run-1", "session-1", "", func() {})
	if err != nil {
		t.Fatalf("activate run: %v", err)
	}

	runWriter := newLifecycleResponseWriter()
	runRequest := httptest.NewRequest(http.MethodGet, "/api/runs/run-1/events", nil)
	runRequest.SetPathValue("id", run.id)
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		server.handleRunEvents(runWriter, runRequest)
	}()
	waitForLifecycleSignal(t, runWriter.flushed, "run stream startup")

	consoleWriter := newLifecycleResponseWriter()
	consoleDone := make(chan struct{})
	go func() {
		defer close(consoleDone)
		server.handleConsoleStream(consoleWriter, httptest.NewRequest(http.MethodGet, "/api/console/stream", nil))
	}()
	waitForLifecycleSignal(t, consoleWriter.flushed, "console stream startup")

	server.Quiesce()
	if _, err := server.runManager.reserve(); !errors.Is(err, ErrRunManagerQuiescing) {
		t.Fatalf("reserve after quiesce = %v, want ErrRunManagerQuiescing", err)
	}
	waitForLifecycleSignal(t, runDone, "run stream shutdown")
	waitForLifecycleSignal(t, consoleDone, "console stream shutdown")

	run.publishTerminal(runStreamItem{name: "run_done", payload: runDoneEvent{Type: "run_done"}})
	run.finishExecution()
	closeServerForTest(t, server)
	closeServerForTest(t, server)
}

func TestServerQuiesceClosesNodeletLogReader(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	runtime, err := runtimestore.Open(filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	defer runtime.Close()
	manager, err := nodelet.NewNodeletManagerWithRuntime(runtime)
	if err != nil {
		t.Fatalf("new nodelet manager: %v", err)
	}
	if err := manager.Add(&nodelet.NodeletConfig{ID: "node-1", Name: "node-1", Address: "http://nodelet", Token: "token"}); err != nil {
		t.Fatalf("add nodelet: %v", err)
	}
	reader := newBlockingReadCloser()
	server := New(Options{
		NodeletManager: manager,
		NodeletClient:  &blockingStreamNodeletClient{stream: reader},
	})
	request := httptest.NewRequest(http.MethodGet, "/api/nodelets/node-1/containers/container-1/logs/stream", nil)
	request.SetPathValue("nodeletID", "node-1")
	request.SetPathValue("containerID", "container-1")
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.handleNodeletLogsStreamRoute(newLifecycleResponseWriter(), request)
	}()
	waitForLifecycleSignal(t, reader.readStarted, "nodelet log read")

	server.Quiesce()
	waitForLifecycleSignal(t, reader.closed, "nodelet log reader close")
	waitForLifecycleSignal(t, done, "nodelet log stream shutdown")
	closeServerForTest(t, server)
}

func TestServerQuiesceTerminatesMCPLogSSE(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	runtime, err := runtimestore.Open(filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	defer runtime.Close()
	manager, err := mcp.NewManagerWithRuntime(runtime, nil)
	if err != nil {
		t.Fatalf("new MCP manager: %v", err)
	}
	if err := manager.Add(mcp.ConnectionConfig{ID: "conn-1", Name: "conn-1", Type: "redis", NodeletID: "node-1", Enabled: false}); err != nil {
		t.Fatalf("add MCP connection: %v", err)
	}

	server := New(Options{})
	server.mcpManager = manager
	writer := newLifecycleResponseWriter()
	request := httptest.NewRequest(http.MethodGet, "/api/mcp/connections/conn-1/logs/stream", nil)
	request.SetPathValue("id", "conn-1")
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.handleMCPLogsStream(writer, request)
	}()
	waitForLifecycleSignal(t, writer.flushed, "MCP log stream startup")

	server.Quiesce()
	waitForLifecycleSignal(t, done, "MCP log stream shutdown")
	closeServerForTest(t, server)
}

func TestServerCloseCanResumeWaitingAfterCallerDeadline(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	server := New(Options{})
	reservation, err := server.runManager.reserve()
	if err != nil {
		t.Fatalf("reserve run: %v", err)
	}
	run, err := reservation.activate("run-1", "session-1", "", func() {})
	if err != nil {
		t.Fatalf("activate run: %v", err)
	}

	shortCtx, cancelShort := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelShort()
	if err := server.Close(shortCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("close with active run = %v, want context deadline exceeded", err)
	}

	run.publishTerminal(runStreamItem{name: "run_done", payload: runDoneEvent{Type: "run_done"}})
	run.finishExecution()
	closeServerForTest(t, server)
}

func TestServerCloseClosesOwnedConsoleHub(t *testing.T) {
	server := New(Options{})
	hub := server.consoleHub

	closeServerForTest(t, server)
	if _, _, err := hub.Subscribe(); !errors.Is(err, loghub.ErrClosed) {
		t.Fatalf("owned console hub subscribe after close = %v, want ErrClosed", err)
	}
}

func TestServerCloseRetainsInjectedConsoleHub(t *testing.T) {
	hub := console.NewHub()
	server := New(Options{ConsoleHub: hub})

	closeServerForTest(t, server)
	entries, cancel, err := hub.Subscribe()
	if err != nil {
		t.Fatalf("injected console hub subscribe after server close: %v", err)
	}
	cancel()
	if entries == nil {
		t.Fatal("injected console hub returned nil subscriber")
	}
	hub.Close()
}
