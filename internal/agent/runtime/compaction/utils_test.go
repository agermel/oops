package compaction

import (
	"encoding/json"
	"strings"
	"testing"

	protocol "oops/internal/agent/ai"
)

func TestSerializeConversationIncludesToolCallsAndTruncatesResults(t *testing.T) {
	messages := protocol.MessageList{
		testUserMessage("request"),
		protocol.AssistantMessage{
			Content: protocol.ContentList{
				protocol.NewThinkingContent("plan"),
				protocol.NewTextContent("working"),
				protocol.NewToolCallContent("call-1", "read", json.RawMessage(`{"path":"file.go","offset":2}`)),
			},
			StopReason: protocol.StopReasonToolUse,
		},
		protocol.ToolResultMessage{
			ToolCallID: "call-1",
			ToolName:   "read",
			Content:    protocol.ContentList{protocol.NewTextContent(strings.Repeat("x", 5000))},
		},
	}
	serialized := SerializeConversation(messages)
	for _, fragment := range []string{
		"[User]: request",
		"[Assistant thinking]: plan",
		"[Assistant]: working",
		`[Assistant tool calls]: read(offset=2, path="file.go")`,
		"[Tool result]:",
		"[... 3000 more characters truncated]",
	} {
		if !strings.Contains(serialized, fragment) {
			t.Fatalf("serialized conversation missing %q: %s", fragment, serialized)
		}
	}
}

func TestFileOperationsComputeSortedReadOnlyAndModifiedLists(t *testing.T) {
	operations := NewFileOperations()
	ExtractFileOperations(testToolCallAssistant("read-1", "read", "z.go"), &operations)
	ExtractFileOperations(testToolCallAssistant("write-1", "write", "z.go"), &operations)
	ExtractFileOperations(testToolCallAssistant("edit-1", "edit", "b.go"), &operations)
	ExtractFileOperations(protocol.ToolResultMessage{
		ToolCallID: "read-2",
		ToolName:   "read",
		Details:    map[string]any{"path": "a.go"},
	}, &operations)

	readFiles, modifiedFiles := ComputeFileLists(operations)
	if strings.Join(readFiles, ",") != "a.go" {
		t.Fatalf("read files = %#v", readFiles)
	}
	if strings.Join(modifiedFiles, ",") != "b.go,z.go" {
		t.Fatalf("modified files = %#v", modifiedFiles)
	}
	formatted := FormatFileOperations(readFiles, modifiedFiles)
	if formatted != "\n\n<read-files>\na.go\n</read-files>\n\n<modified-files>\nb.go\nz.go\n</modified-files>" {
		t.Fatalf("formatted details = %q", formatted)
	}
}
