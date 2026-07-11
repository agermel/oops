package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"oops/internal/api"

	"go.uber.org/goleak"
)

type singleConnectionListener struct {
	conn       net.Conn
	closed     chan struct{}
	acceptOnce sync.Once
	closeOnce  sync.Once
}

func newSingleConnectionListener(conn net.Conn) *singleConnectionListener {
	return &singleConnectionListener{
		conn:   conn,
		closed: make(chan struct{}),
	}
}

func (l *singleConnectionListener) Accept() (net.Conn, error) {
	var conn net.Conn
	accepted := false
	l.acceptOnce.Do(func() {
		conn = l.conn
		accepted = true
	})
	if accepted {
		return conn, nil
	}
	<-l.closed
	return nil, net.ErrClosed
}

func (l *singleConnectionListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (l *singleConnectionListener) Addr() net.Addr { return l.conn.LocalAddr() }

type writeSignalResponseWriter struct {
	http.ResponseWriter
	started chan struct{}
	once    sync.Once
}

func (w *writeSignalResponseWriter) Write(data []byte) (int, error) {
	w.once.Do(func() { close(w.started) })
	return w.ResponseWriter.Write(data)
}

func (w *writeSignalResponseWriter) Flush() {
	w.ResponseWriter.(http.Flusher).Flush()
}

func (w *writeSignalResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func TestShutdownAPIServerForceClosesBlockedSSE(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	apiServer := api.New(api.Options{})
	serverConn, clientConn := net.Pipe()
	listener := newSingleConnectionListener(serverConn)
	writeStarted := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiServer.Routes().ServeHTTP(&writeSignalResponseWriter{
			ResponseWriter: w,
			started:        writeStarted,
		}, r)
	})
	httpServer := &http.Server{Handler: handler}
	serveDone := make(chan error, 1)
	go func() { serveDone <- httpServer.Serve(listener) }()
	defer func() {
		_ = clientConn.Close()
		_ = httpServer.Close()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = apiServer.Close(ctx)
	}()

	requestDone := make(chan error, 1)
	go func() {
		_, err := clientConn.Write([]byte("GET /api/console/stream HTTP/1.1\r\nHost: example.test\r\n\r\n"))
		requestDone <- err
	}()

	select {
	case <-writeStarted:
	case <-time.After(time.Second):
		t.Fatal("console SSE write did not start")
	}
	select {
	case err := <-requestDone:
		if err != nil {
			t.Fatalf("write request: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("request write did not finish")
	}

	httpErr, closeErr := shutdownAPIServer(httpServer, apiServer, 20*time.Millisecond)
	if !errors.Is(httpErr, context.DeadlineExceeded) {
		t.Fatalf("HTTP shutdown error = %v, want context deadline exceeded", httpErr)
	}
	if closeErr != nil {
		t.Fatalf("API close error = %v", closeErr)
	}

	select {
	case err := <-serveDone:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("Serve error = %v, want http.ErrServerClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP server did not exit after forced close")
	}
}
