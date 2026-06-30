package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestWriteJSON 测试 writeJSON 对 map 的输出。
func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, map[string]any{"key": "value", "num": 42})

	resp := w.Result()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["key"] != "value" {
		t.Errorf("body[key] = %v, want %q", body["key"], "value")
	}
	if v, ok := body["num"].(float64); !ok || v != 42 {
		t.Errorf("body[num] = %v, want 42", body["num"])
	}
}

// TestWriteJSONError 测试 writeJSONError 的错误 JSON 响应。
func TestWriteJSONError(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSONError(w, "something broke", 500)

	resp := w.Result()
	if resp.StatusCode != 500 {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != "something broke" {
		t.Errorf("body[error] = %q, want %q", body["error"], "something broke")
	}
}

// TestWriteJSONError_BadRequest 测试 writeJSONError 的 400 状态码。
func TestWriteJSONError_BadRequest(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSONError(w, "bad input", 400)

	resp := w.Result()
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != "bad input" {
		t.Errorf("body[error] = %q, want %q", body["error"], "bad input")
	}
}

// TestWriteJSONOK 测试 writeJSONOK 输出 {"status":"ok"}。
func TestWriteJSONOK(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSONOK(w)

	resp := w.Result()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("body[status] = %q, want %q", body["status"], "ok")
	}
}

// TestWriteJSON_SpecialChars 测试包含特殊字符的 JSON 编码。
func TestWriteJSON_SpecialChars(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, map[string]string{
		"unicode":   "中文测试",
		"quotes":    `he said "hello"`,
		"backslash": `path\to\file`,
	})

	resp := w.Result()
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["unicode"] != "中文测试" {
		t.Errorf("unicode = %q", body["unicode"])
	}
	if body["quotes"] != `he said "hello"` {
		t.Errorf("quotes = %q", body["quotes"])
	}
	if body["backslash"] != `path\to\file` {
		t.Errorf("backslash = %q", body["backslash"])
	}
}
