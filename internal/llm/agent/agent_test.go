package agent

import (
	"errors"
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

func TestPendingToolErrorMessagesCloseToolCalls(t *testing.T) {
	pending := []pendingToolCall{{
		ID:   "call-1",
		Name: "etcd_list",
	}}

	msgs := pendingToolErrorMessages(pending, errors.New("failed to call tool"))
	if len(msgs) != 1 {
		t.Fatalf("len(msgs) = %d, want 1", len(msgs))
	}
	if msgs[0].Role != schema.Tool {
		t.Fatalf("Role = %q, want tool", msgs[0].Role)
	}
	if msgs[0].ToolCallID != "call-1" {
		t.Fatalf("ToolCallID = %q, want call-1", msgs[0].ToolCallID)
	}
	if msgs[0].ToolName != "etcd_list" {
		t.Fatalf("ToolName = %q, want etcd_list", msgs[0].ToolName)
	}
	if msgs[0].Content != "failed to call tool" {
		t.Fatalf("Content = %q", msgs[0].Content)
	}
}

func TestTrackPendingToolCallsRemovesToolResult(t *testing.T) {
	pending := trackPendingToolCalls(nil, &schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{{
			ID: "call-1",
			Function: schema.FunctionCall{
				Name: "etcd_list",
			},
		}},
	})
	if len(pending) != 1 {
		t.Fatalf("len(pending) = %d, want 1", len(pending))
	}

	pending = trackPendingToolCalls(pending, &schema.Message{
		Role:       schema.Tool,
		ToolCallID: "call-1",
		ToolName:   "etcd_list",
		Content:    "ok",
	})
	if len(pending) != 0 {
		t.Fatalf("len(pending) = %d, want 0", len(pending))
	}
}
