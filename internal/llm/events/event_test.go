package events

import (
	"encoding/json"
	"testing"
)

func TestStepEventJSONSnapshot(t *testing.T) {
	event := StepEvent{
		Type:       "tool_call",
		Content:    "repo_sync",
		ToolName:   "repo_sync",
		ToolArgs:   `{"projectId":"proj-1"}`,
		ToolCallID: "call-1",
		AgentType:  "default",
		MaxStep:    15,
		Tokens:     123,
		Trimmed:    2,
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"type":"tool_call","content":"repo_sync","toolName":"repo_sync","toolArgs":"{\"projectId\":\"proj-1\"}","toolCallId":"call-1","agentType":"default","maxStep":15,"tokens":123,"trimmed":2}`
	if string(data) != want {
		t.Fatalf("StepEvent JSON = %s, want %s", data, want)
	}
}
