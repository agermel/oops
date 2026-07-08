package events

import (
	"encoding/json"
	"testing"
)

func TestStepEventJSONSnapshot(t *testing.T) {
	tests := []struct {
		name  string
		event StepEvent
		want  string
	}{
		{
			name:  "thinking",
			event: StepEvent{Type: "thinking", Content: "checking"},
			want:  `{"type":"thinking","content":"checking"}`,
		},
		{
			name: "tool_call",
			event: StepEvent{
				Type:       "tool_call",
				Content:    "repo_sync",
				ToolName:   "repo_sync",
				ToolArgs:   `{"projectId":"proj-1"}`,
				ToolCallID: "call-1",
			},
			want: `{"type":"tool_call","content":"repo_sync","toolName":"repo_sync","toolArgs":"{\"projectId\":\"proj-1\"}","toolCallId":"call-1"}`,
		},
		{
			name: "tool_result",
			event: StepEvent{
				Type:       "tool_result",
				Content:    "ok",
				ToolName:   "repo_sync",
				ToolCallID: "call-1",
			},
			want: `{"type":"tool_result","content":"ok","toolName":"repo_sync","toolCallId":"call-1"}`,
		},
		{
			name:  "answer",
			event: StepEvent{Type: "answer", Content: "done"},
			want:  `{"type":"answer","content":"done"}`,
		},
		{
			name:  "error",
			event: StepEvent{Type: "error", Content: "failed"},
			want:  `{"type":"error","content":"failed"}`,
		},
		{
			name: "session",
			event: StepEvent{
				Type:      "session",
				Content:   "sess-1",
				AgentType: "default",
				MaxStep:   15,
			},
			want: `{"type":"session","content":"sess-1","agentType":"default","maxStep":15}`,
		},
		{
			name:  "stats",
			event: StepEvent{Type: "stats", Tokens: 123, Trimmed: 2},
			want:  `{"type":"stats","content":"","tokens":123,"trimmed":2}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.event)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if string(data) != tt.want {
				t.Fatalf("StepEvent JSON = %s, want %s", data, tt.want)
			}
		})
	}
}
