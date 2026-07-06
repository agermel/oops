package agent

import (
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestMessageToStepEventsKeepsToolArgsAsJSON(t *testing.T) {
	msg := &schema.Message{
		Content: "查询机器列表",
		ToolCalls: []schema.ToolCall{{
			ID: "call-1",
			Function: schema.FunctionCall{
				Name:      "list_nodelets",
				Arguments: "{}",
			},
		}},
	}

	events := messageToStepEvents(msg)
	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(events))
	}

	call := events[1]
	if call.Type != "tool_call" {
		t.Fatalf("call.Type = %q, want tool_call", call.Type)
	}
	if call.ToolArgs != "{}" {
		t.Fatalf("call.ToolArgs = %q, want raw JSON object", call.ToolArgs)
	}
	if call.ToolCallID != "call-1" {
		t.Fatalf("call.ToolCallID = %q, want call-1", call.ToolCallID)
	}
}

func TestMessageToStepEventsSkipsTextOnlyAssistantMessage(t *testing.T) {
	msg := &schema.Message{Content: "最终回答"}

	events := messageToStepEvents(msg)
	if len(events) != 0 {
		t.Fatalf("len(events) = %d, want 0", len(events))
	}
}
