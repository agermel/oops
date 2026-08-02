package model

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	protocol "oops/internal/agent/ai"
	"oops/internal/config"
)

func TestRequestOptionsAreCloned(t *testing.T) {
	headers := map[string]string{"X-Initial": "one"}
	client := newRequestTestClient(t, "http://example.invalid", RequestOptions{Headers: headers})
	headers["X-Initial"] = "mutated"

	snapshot := client.RequestOptions()
	if snapshot.Headers["X-Initial"] != "one" {
		t.Fatalf("initial header = %q, want one", snapshot.Headers["X-Initial"])
	}
	snapshot.Headers["X-Initial"] = "changed"
	if got := client.RequestOptions().Headers["X-Initial"]; got != "one" {
		t.Fatalf("stored header = %q, want one", got)
	}

	updated := map[string]string{"X-Updated": "two"}
	if err := client.SetRequestOptions(RequestOptions{Headers: updated}); err != nil {
		t.Fatal(err)
	}
	updated["X-Updated"] = "mutated"
	if got := client.RequestOptions().Headers["X-Updated"]; got != "two" {
		t.Fatalf("updated header = %q, want two", got)
	}
}

func TestStreamSnapshotsOptionsPerRequest(t *testing.T) {
	receivedHeaders := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders <- r.Header.Get("X-Turn")
		writeRequestTestStream(w)
	}))
	defer server.Close()

	hookStarted := make(chan struct{})
	releaseHook := make(chan struct{})
	client := newRequestTestClient(t, server.URL, RequestOptions{
		Headers: map[string]string{"X-Turn": "first"},
		PayloadHook: func(_ context.Context, _ protocol.StreamRequest, payload json.RawMessage) (json.RawMessage, error) {
			close(hookStarted)
			<-releaseHook
			return payload, nil
		},
	})

	firstDone := make(chan error, 1)
	go func() {
		_, err := runRequestTestStream(context.Background(), client.Stream(), "session-first")
		firstDone <- err
	}()

	<-hookStarted
	if err := client.SetRequestOptions(RequestOptions{Headers: map[string]string{"X-Turn": "second"}}); err != nil {
		t.Fatal(err)
	}
	close(releaseHook)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if _, err := runRequestTestStream(t.Context(), client.Stream(), "session-second"); err != nil {
		t.Fatal(err)
	}

	if got := <-receivedHeaders; got != "first" {
		t.Fatalf("first request header = %q, want first", got)
	}
	if got := <-receivedHeaders; got != "second" {
		t.Fatalf("second request header = %q, want second", got)
	}
}

func TestPayloadAndResponseHooks(t *testing.T) {
	payloads := make(chan map[string]json.RawMessage, 1)
	requestHeaders := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			return
		}
		payloads <- payload
		requestHeaders <- r.Header.Get("X-Request")
		w.Header().Set("X-Response-ID", "response-1")
		writeRequestTestStream(w)
	}))
	defer server.Close()

	responses := make(chan ResponseInfo, 1)
	client := newRequestTestClient(t, server.URL, RequestOptions{
		Headers: map[string]string{"X-Request": "request-1"},
		PayloadHook: func(_ context.Context, request protocol.StreamRequest, payload json.RawMessage) (json.RawMessage, error) {
			if request.SessionID != "session-hook" {
				return nil, errors.New("payload hook received the wrong session")
			}
			var body map[string]json.RawMessage
			if err := json.Unmarshal(payload, &body); err != nil {
				return nil, err
			}
			body["request_marker"] = json.RawMessage(`"payload-hook"`)
			return json.Marshal(body)
		},
		ResponseHook: func(_ context.Context, request protocol.StreamRequest, response ResponseInfo) error {
			if request.SessionID != "session-hook" {
				return errors.New("response hook received the wrong session")
			}
			responses <- response
			return nil
		},
	})

	if _, err := runRequestTestStream(t.Context(), client.Stream(), "session-hook"); err != nil {
		t.Fatal(err)
	}
	payload := <-payloads
	if got := string(payload["request_marker"]); got != `"payload-hook"` {
		t.Fatalf("request marker = %s", got)
	}
	if got := <-requestHeaders; got != "request-1" {
		t.Fatalf("request header = %q, want request-1", got)
	}
	response := <-responses
	if response.StatusCode != http.StatusOK {
		t.Fatalf("response status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if got := response.Headers.Get("X-Response-ID"); got != "response-1" {
		t.Fatalf("response header = %q, want response-1", got)
	}
}

func TestPayloadHookNilPreservesOriginalPayload(t *testing.T) {
	payloads := make(chan map[string]json.RawMessage, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request payload: %v", err)
			return
		}
		payloads <- payload
		writeRequestTestStream(w)
	}))
	defer server.Close()

	client := newRequestTestClient(t, server.URL, RequestOptions{
		PayloadHook: func(context.Context, protocol.StreamRequest, json.RawMessage) (json.RawMessage, error) {
			return nil, nil
		},
	})
	if _, err := runRequestTestStream(t.Context(), client.Stream(), "session-nil-payload"); err != nil {
		t.Fatal(err)
	}
	payload := <-payloads
	if _, ok := payload["messages"]; !ok {
		t.Fatalf("request payload = %#v, want original messages", payload)
	}
}

func TestRequestTimeoutIsIndependent(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			select {
			case <-r.Context().Done():
			case <-time.After(200 * time.Millisecond):
			}
			return
		}
		writeRequestTestStream(w)
	}))
	defer server.Close()

	client := newRequestTestClient(t, server.URL, RequestOptions{Timeout: 30 * time.Millisecond})
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := time.Now()
	message, err := runRequestTestStream(parent, client.Stream(), "session-timeout")
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= 150*time.Millisecond {
		t.Fatalf("request timeout elapsed = %s, want under 150ms", elapsed)
	}
	if message.StopReason != protocol.StopReasonAborted {
		t.Fatalf("timeout stop reason = %q, want %q", message.StopReason, protocol.StopReasonAborted)
	}
	if parent.Err() != nil {
		t.Fatalf("parent context error = %v", parent.Err())
	}

	if err := client.SetRequestOptions(RequestOptions{}); err != nil {
		t.Fatal(err)
	}
	message, err = runRequestTestStream(t.Context(), client.Stream(), "session-next")
	if err != nil {
		t.Fatal(err)
	}
	if message.StopReason != protocol.StopReasonStop {
		t.Fatalf("next request stop reason = %q, want %q", message.StopReason, protocol.StopReasonStop)
	}
}

func TestResponseHookErrorStopsRequest(t *testing.T) {
	wantErr := errors.New("response rejected")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeRequestTestStream(w)
	}))
	defer server.Close()

	client := newRequestTestClient(t, server.URL, RequestOptions{
		ResponseHook: func(context.Context, protocol.StreamRequest, ResponseInfo) error {
			return wantErr
		},
	})
	_, err := client.Stream()(t.Context(), requestTestStreamRequest("session-error"))
	if !errors.Is(err, wantErr) {
		t.Fatalf("stream error = %v, want %v", err, wantErr)
	}
}

func TestRejectsNegativeRequestTimeout(t *testing.T) {
	_, err := New(t.Context(), config.LLMConfig{
		Provider: "openai-compatible",
		Model:    "test-model",
		BaseURL:  "http://example.invalid",
		APIKey:   "test-key",
	}, Options{Request: RequestOptions{Timeout: -time.Second}})
	if err == nil {
		t.Fatal("New() error is nil")
	}
}

func newRequestTestClient(t *testing.T, baseURL string, options RequestOptions) *Client {
	t.Helper()
	client, err := New(t.Context(), config.LLMConfig{
		Provider: "openai-compatible",
		Model:    "test-model",
		BaseURL:  baseURL,
		APIKey:   "test-key",
	}, Options{Request: options})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func runRequestTestStream(ctx context.Context, streamFunc protocol.StreamFunc, sessionID string) (*protocol.AssistantMessage, error) {
	stream, err := streamFunc(ctx, requestTestStreamRequest(sessionID))
	if err != nil {
		return nil, err
	}
	for range stream.Events() {
	}
	return stream.Result(ctx)
}

func requestTestStreamRequest(sessionID string) protocol.StreamRequest {
	return protocol.StreamRequest{
		SessionID: sessionID,
		Context: protocol.Context{
			Messages: protocol.MessageList{
				protocol.UserMessage{Content: protocol.ContentList{protocol.NewTextContent("hello")}},
			},
		},
	}
}

func writeRequestTestStream(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = w.Write([]byte("data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"2\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
}
