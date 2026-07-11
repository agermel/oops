package main

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv("ETCD_ENDPOINTS", "")
	t.Setenv("ETCD_USERNAME", "")
	t.Setenv("ETCD_PASSWORD", "")
	t.Setenv("ETCD_DIAL_TIMEOUT", "")

	got := loadConfig()
	want := config{
		Endpoints:   []string{"localhost:2379"},
		DialTimeout: 5 * time.Second,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loadConfig() = %#v, want %#v", got, want)
	}
}

func TestLoadConfigOverrides(t *testing.T) {
	t.Setenv("ETCD_ENDPOINTS", "etcd-a:2379,etcd-b:2379")
	t.Setenv("ETCD_USERNAME", "operator")
	t.Setenv("ETCD_PASSWORD", "secret")
	t.Setenv("ETCD_DIAL_TIMEOUT", "12s")

	got := loadConfig()
	want := config{
		Endpoints:   []string{"etcd-a:2379", "etcd-b:2379"},
		Username:    "operator",
		Password:    "secret",
		DialTimeout: 12 * time.Second,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loadConfig() = %#v, want %#v", got, want)
	}
}

func TestLoadConfigInvalidTimeoutUsesDefault(t *testing.T) {
	t.Setenv("ETCD_DIAL_TIMEOUT", "invalid")

	if got := loadConfig().DialTimeout; got != 5*time.Second {
		t.Fatalf("loadConfig().DialTimeout = %s, want %s", got, 5*time.Second)
	}
}

func TestContextWithTimeout(t *testing.T) {
	before := time.Now()
	ctx, cancel := ctxWithTimeout(context.Background(), 0)
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("ctxWithTimeout() returned a context without a deadline")
	}
	if duration := deadline.Sub(before); duration < 9*time.Second || duration > 11*time.Second {
		t.Fatalf("default timeout = %s, want about 10s", duration)
	}
}

func TestDecodeArgs(t *testing.T) {
	type args struct {
		Key   string `json:"key"`
		Limit int    `json:"limit"`
	}

	var got args
	if err := decodeArgs(map[string]any{"key": "config/app", "limit": 3}, &got); err != nil {
		t.Fatalf("decodeArgs() error = %v", err)
	}
	if want := (args{Key: "config/app", Limit: 3}); got != want {
		t.Fatalf("decodeArgs() = %#v, want %#v", got, want)
	}

	if err := decodeArgs(map[string]any{"limit": "many"}, &got); err == nil {
		t.Fatal("decodeArgs() error = nil, want type error")
	}
}

func TestResultHelpers(t *testing.T) {
	result, err := jsonResult(map[string]any{"key": "config/app", "count": 1})
	if err != nil {
		t.Fatalf("jsonResult() error = %v", err)
	}
	if result.IsError {
		t.Fatal("jsonResult() returned an error result")
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(resultText(t, result)), &payload); err != nil {
		t.Fatalf("jsonResult() returned invalid JSON: %v", err)
	}
	if payload["key"] != "config/app" || payload["count"] != float64(1) {
		t.Fatalf("jsonResult() payload = %#v", payload)
	}

	errResult := errorResult("connect: %s", "unavailable")
	if !errResult.IsError {
		t.Fatal("errorResult() did not mark the result as an error")
	}
	if got, want := resultText(t, errResult), "connect: unavailable"; got != want {
		t.Fatalf("errorResult() text = %q, want %q", got, want)
	}
}

func resultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if len(result.Content) != 1 {
		t.Fatalf("result contains %d content items, want 1", len(result.Content))
	}
	content, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("result content type = %T, want text", result.Content[0])
	}
	return content.Text
}
