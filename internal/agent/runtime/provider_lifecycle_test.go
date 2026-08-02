package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	protocol "oops/internal/agent/ai"
	"oops/internal/agent/runtime/model"
	"oops/internal/config"
)

func TestAgentHarnessProviderOptionsAreIsolatedAcrossConcurrentRuns(t *testing.T) {
	type requestRecord struct {
		header string
		marker string
	}
	received := make(chan requestRecord, 2)
	requestErrors := make(chan error, 2)
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			requestErrors <- err
			return
		}
		var marker string
		if err := json.Unmarshal(payload["harness_marker"], &marker); err != nil {
			requestErrors <- err
			return
		}
		received <- requestRecord{header: r.Header.Get("X-Harness"), marker: marker}
		arrived <- struct{}{}
		<-release
		writeProviderTextStream(w, "ok")
	}))
	defer server.Close()

	client := newProviderLifecycleClient(t, server.URL)
	runtime := NewRuntime(RuntimeOptions{})
	headersA := map[string]string{"X-Harness": "a"}
	if err := client.SetRequestOptions(model.RequestOptions{
		Headers:     headersA,
		PayloadHook: providerMarkerHook("provider-session-a", "a"),
	}); err != nil {
		t.Fatal(err)
	}
	harnessA := prepareProviderLifecycleHarness(t, runtime, client, "provider-session-a")
	headersA["X-Harness"] = "mutated"
	if err := client.SetRequestOptions(model.RequestOptions{
		Headers:     map[string]string{"X-Harness": "b"},
		PayloadHook: providerMarkerHook("provider-session-b", "b"),
	}); err != nil {
		t.Fatal(err)
	}
	harnessB := prepareProviderLifecycleHarness(t, runtime, client, "provider-session-b")
	if err := client.SetRequestOptions(model.RequestOptions{Headers: map[string]string{"X-Harness": "shared"}}); err != nil {
		t.Fatal(err)
	}

	optionsA := harnessA.ProviderRequestOptions()
	if got := optionsA.Headers["X-Harness"]; got != "a" {
		t.Fatalf("harness A header = %q, want a", got)
	}
	optionsA.Headers["X-Harness"] = "changed"
	if got := harnessA.ProviderRequestOptions().Headers["X-Harness"]; got != "a" {
		t.Fatalf("stored harness A header = %q, want a", got)
	}

	if got := harnessB.ProviderRequestOptions().Headers["X-Harness"]; got != "b" {
		t.Fatalf("harness B header = %q, want b", got)
	}

	runErrors := make(chan error, 2)
	go func() { runErrors <- harnessA.PromptText(t.Context(), "alpha", 1) }()
	go func() { runErrors <- harnessB.PromptText(t.Context(), "beta", 2) }()
	for range 2 {
		select {
		case <-arrived:
		case err := <-requestErrors:
			t.Fatal(err)
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent provider requests did not arrive")
		}
	}
	close(release)
	for range 2 {
		if err := <-runErrors; err != nil {
			t.Fatal(err)
		}
	}

	seen := map[string]string{}
	for range 2 {
		record := <-received
		seen[record.header] = record.marker
	}
	if seen["a"] != "a" || seen["b"] != "b" {
		t.Fatalf("provider request isolation = %#v, want a:a and b:b", seen)
	}
}

func TestAgentHarnessProviderOptionsUpdateOnNextTurn(t *testing.T) {
	requestHeaders := make(chan string, 2)
	firstArrived := make(chan struct{})
	releaseFirst := make(chan struct{})
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestHeaders <- r.Header.Get("X-Turn")
		if requestCount.Add(1) == 1 {
			close(firstArrived)
			<-releaseFirst
			writeProviderToolCallStream(w)
			return
		}
		writeProviderTextStream(w, "done")
	}))
	defer server.Close()

	client := newProviderLifecycleClient(t, server.URL)
	harness := prepareProviderLifecycleHarness(t, NewRuntime(RuntimeOptions{}), client, "provider-turn-session")
	if err := harness.SetProviderRequestOptions(model.RequestOptions{Headers: map[string]string{"X-Turn": "first"}}); err != nil {
		t.Fatal(err)
	}

	runDone := make(chan error, 1)
	go func() { runDone <- harness.PromptText(t.Context(), "run two turns", 1) }()
	select {
	case <-firstArrived:
	case <-time.After(5 * time.Second):
		t.Fatal("first provider request did not arrive")
	}
	if err := harness.SetProviderRequestOptions(model.RequestOptions{Headers: map[string]string{"X-Turn": "second"}}); err != nil {
		t.Fatal(err)
	}
	close(releaseFirst)
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("provider turn run did not finish")
	}

	if got := <-requestHeaders; got != "first" {
		t.Fatalf("first turn header = %q, want first", got)
	}
	if got := <-requestHeaders; got != "second" {
		t.Fatalf("second turn header = %q, want second", got)
	}
}

func TestAgentHarnessProviderOptionsRefreshOnSameClientRebind(t *testing.T) {
	client := newProviderLifecycleClient(t, "http://example.invalid")
	if err := client.SetRequestOptions(model.RequestOptions{Headers: map[string]string{"X-Rebind": "first"}}); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(RuntimeOptions{})
	harness := prepareProviderLifecycleHarness(t, runtime, client, "provider-rebind-session")
	if err := harness.SetProviderRequestOptions(model.RequestOptions{Headers: map[string]string{"X-Rebind": "local"}}); err != nil {
		t.Fatal(err)
	}
	if err := client.SetRequestOptions(model.RequestOptions{Headers: map[string]string{"X-Rebind": "second"}}); err != nil {
		t.Fatal(err)
	}

	rebound, err := runtime.PreparePromptHarness(t.Context(), PreparePromptOptions{
		SessionID: "provider-rebind-session",
		Model:     "test-model",
		Provider:  client.Provider(),
		MaxTurns:  4,
		Client:    client,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rebound != harness {
		t.Fatal("same session rebind returned a different harness")
	}
	if got := rebound.ProviderRequestOptions().Headers["X-Rebind"]; got != "second" {
		t.Fatalf("rebound request header = %q, want second", got)
	}
}

func TestAgentHarnessEmitsProviderLifecycleAndPreservesHooks(t *testing.T) {
	requestPayloads := make(chan map[string]json.RawMessage, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode provider payload: %v", err)
			return
		}
		requestPayloads <- payload
		w.Header().Set("X-Response-ID", "response-1")
		writeProviderTextStream(w, "ok")
	}))
	defer server.Close()

	client := newProviderLifecycleClient(t, server.URL)
	harness := prepareProviderLifecycleHarness(t, NewRuntime(RuntimeOptions{}), client, "provider-event-session")
	var payloadHookCalls atomic.Int32
	responseHookCalls := make(chan model.ResponseInfo, 1)
	if err := harness.SetProviderRequestOptions(model.RequestOptions{
		Headers: map[string]string{"X-Request": "request-1"},
		PayloadHook: func(_ context.Context, request protocol.StreamRequest, _ json.RawMessage) (json.RawMessage, error) {
			if request.SessionID != "provider-event-session" {
				return nil, fmt.Errorf("payload hook session = %q", request.SessionID)
			}
			payloadHookCalls.Add(1)
			return nil, nil
		},
		ResponseHook: func(_ context.Context, request protocol.StreamRequest, response model.ResponseInfo) error {
			if request.SessionID != "provider-event-session" {
				return fmt.Errorf("response hook session = %q", request.SessionID)
			}
			responseHookCalls <- response
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var lifecycle []RunEvent
	harness.ListenRunEvents(func(_ context.Context, event RunEvent) error {
		switch event.EventName() {
		case "before_provider_request", "before_provider_payload", "after_provider_response":
			mu.Lock()
			lifecycle = append(lifecycle, event)
			mu.Unlock()
		}
		return nil
	})
	if err := harness.PromptText(t.Context(), "observe provider lifecycle", 1); err != nil {
		t.Fatal(err)
	}
	if payloadHookCalls.Load() != 1 {
		t.Fatalf("payload hook calls = %d, want 1", payloadHookCalls.Load())
	}
	response := <-responseHookCalls
	if response.StatusCode != http.StatusOK || response.Headers.Get("X-Response-ID") != "response-1" {
		t.Fatalf("response hook info = %#v", response)
	}
	payload := <-requestPayloads
	if _, ok := payload["messages"]; !ok {
		t.Fatalf("provider payload = %#v, want original messages", payload)
	}

	mu.Lock()
	events := append([]RunEvent(nil), lifecycle...)
	mu.Unlock()
	if len(events) != 3 {
		t.Fatalf("provider lifecycle events = %#v, want 3", events)
	}
	requestEvent, ok := events[0].(BeforeProviderRequestEvent)
	if !ok || requestEvent.SessionID != "provider-event-session" || requestEvent.Headers["X-Request"] != "request-1" {
		t.Fatalf("before provider request = %#v", events[0])
	}
	payloadEvent, ok := events[1].(BeforeProviderPayloadEvent)
	if !ok || payloadEvent.SessionID != "provider-event-session" || !json.Valid(payloadEvent.Payload) {
		t.Fatalf("before provider payload = %#v", events[1])
	}
	responseEvent, ok := events[2].(AfterProviderResponseEvent)
	if !ok || responseEvent.Status != http.StatusOK || responseEvent.Headers.Get("X-Response-ID") != "response-1" {
		t.Fatalf("after provider response = %#v", events[2])
	}
}

func TestAgentHarnessSummaryPhasesUseCurrentProviderOptions(t *testing.T) {
	receivedHeaders := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders <- r.Header.Get("X-Summary")
		writeProviderTextStream(w, "summary")
	}))
	defer server.Close()

	client := newProviderLifecycleClient(t, server.URL)
	harness := prepareProviderLifecycleHarness(t, NewRuntime(RuntimeOptions{}), client, "provider-summary-session")
	if err := harness.SetProviderRequestOptions(model.RequestOptions{Headers: map[string]string{"X-Summary": "stale-turn"}}); err != nil {
		t.Fatal(err)
	}
	harness.mu.Lock()
	harness.phase = AgentHarnessPhaseTurn
	harness.captureProviderTurnOptionsLocked()
	harness.mu.Unlock()
	defer func() {
		harness.mu.Lock()
		harness.phase = AgentHarnessPhaseIdle
		harness.mu.Unlock()
	}()

	tests := []struct {
		phase  AgentHarnessPhase
		header string
	}{
		{phase: AgentHarnessPhaseCompaction, header: "compaction-current"},
		{phase: AgentHarnessPhaseBranchSummary, header: "branch-current"},
	}
	for _, test := range tests {
		if err := harness.SetProviderRequestOptions(model.RequestOptions{Headers: map[string]string{"X-Summary": test.header}}); err != nil {
			t.Fatal(err)
		}
		harness.mu.Lock()
		harness.phase = test.phase
		harness.mu.Unlock()
		stream, err := harness.providerStream(t.Context(), protocol.StreamRequest{
			Context: protocol.Context{Messages: protocol.MessageList{protocol.UserMessage{
				Content:   protocol.ContentList{protocol.NewTextContent("summarize")},
				Timestamp: 1,
			}}},
			Model:     "test-model",
			Provider:  client.Provider(),
			SessionID: "provider-summary-session",
		})
		if err != nil {
			t.Fatal(err)
		}
		for range stream.Events() {
		}
		if _, err := stream.Result(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := <-receivedHeaders; got != test.header {
			t.Fatalf("%s header = %q, want %q", test.phase, got, test.header)
		}
	}
}

func newProviderLifecycleClient(t *testing.T, baseURL string) *model.Client {
	t.Helper()
	client, err := model.New(t.Context(), config.LLMConfig{
		Provider: "openai-compatible",
		Model:    "test-model",
		BaseURL:  baseURL,
		APIKey:   "test-key",
	}, model.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func prepareProviderLifecycleHarness(t *testing.T, runtime *Runtime, client *model.Client, sessionID string) *AgentHarness {
	t.Helper()
	harness, err := runtime.PreparePromptHarness(t.Context(), PreparePromptOptions{
		NewSessionID: sessionID,
		Model:        "test-model",
		Provider:     client.Provider(),
		MaxTurns:     4,
		Client:       client,
	})
	if err != nil {
		t.Fatal(err)
	}
	return harness
}

func providerMarkerHook(sessionID, marker string) model.PayloadHook {
	return func(_ context.Context, request protocol.StreamRequest, payload json.RawMessage) (json.RawMessage, error) {
		if request.SessionID != sessionID {
			return nil, fmt.Errorf("payload hook session = %q, want %q", request.SessionID, sessionID)
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal(payload, &body); err != nil {
			return nil, err
		}
		encodedMarker, err := json.Marshal(marker)
		if err != nil {
			return nil, err
		}
		body["harness_marker"] = encodedMarker
		return json.Marshal(body)
	}
}

func writeProviderTextStream(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprintf(w, "data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":%q},\"finish_reason\":null}]}\n\ndata: {\"id\":\"2\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", text)
}

func writeProviderToolCallStream(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = w.Write([]byte("data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"ls\",\"arguments\":\"{\\\"path\\\":\\\".\\\"}\"}}]},\"finish_reason\":null}]}\n\ndata: {\"id\":\"2\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"))
}
