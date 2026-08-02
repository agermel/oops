package compaction

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	protocol "oops/internal/agent/ai"
	"oops/internal/agent/runtime/session"
)

const toolResultMaxChars = 2000

// FileOperations records file access found in a summarized history range.
type FileOperations struct {
	Read    map[string]struct{}
	Written map[string]struct{}
	Edited  map[string]struct{}
}

func NewFileOperations() FileOperations {
	return FileOperations{
		Read:    map[string]struct{}{},
		Written: map[string]struct{}{},
		Edited:  map[string]struct{}{},
	}
}

// ExtractFileOperations adds workspace file operations from one protocol message.
func ExtractFileOperations(message protocol.AgentMessage, operations *FileOperations) {
	if operations == nil || message == nil {
		return
	}
	ensureFileOperations(operations)

	if assistant, ok := protocol.AsAssistantMessage(message); ok {
		for _, call := range protocol.ToolCallsFromAssistant(assistant) {
			addToolPath(operations, call.Name, pathFromJSON(call.Arguments))
		}
		return
	}

	if result, ok := protocol.AsToolResultMessage(message); ok {
		addToolPath(operations, result.ToolName, pathFromDetails(result.Details))
	}
}

// ComputeFileLists returns sorted read-only and modified paths.
func ComputeFileLists(operations FileOperations) (readFiles, modifiedFiles []string) {
	modified := make(map[string]struct{}, len(operations.Written)+len(operations.Edited))
	for path := range operations.Written {
		modified[path] = struct{}{}
	}
	for path := range operations.Edited {
		modified[path] = struct{}{}
	}
	for path := range operations.Read {
		if _, changed := modified[path]; !changed {
			readFiles = append(readFiles, path)
		}
	}
	for path := range modified {
		modifiedFiles = append(modifiedFiles, path)
	}
	sort.Strings(readFiles)
	sort.Strings(modifiedFiles)
	return readFiles, modifiedFiles
}

// FormatFileOperations appends machine-readable file metadata to a summary.
func FormatFileOperations(readFiles, modifiedFiles []string) string {
	sections := make([]string, 0, 2)
	if len(readFiles) > 0 {
		sections = append(sections, "<read-files>\n"+strings.Join(readFiles, "\n")+"\n</read-files>")
	}
	if len(modifiedFiles) > 0 {
		sections = append(sections, "<modified-files>\n"+strings.Join(modifiedFiles, "\n")+"\n</modified-files>")
	}
	if len(sections) == 0 {
		return ""
	}
	return "\n\n" + strings.Join(sections, "\n\n")
}

// SerializeConversation renders protocol messages as compact summary input.
func SerializeConversation(messages protocol.MessageList) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		if user, ok := protocol.AsUserMessage(message); ok {
			if text := textFromContent(user.Content); text != "" {
				parts = append(parts, "[User]: "+text)
			}
			continue
		}
		if assistant, ok := protocol.AsAssistantMessage(message); ok {
			parts = append(parts, serializeAssistant(assistant)...)
			continue
		}
		if result, ok := protocol.AsToolResultMessage(message); ok {
			if text := textFromContent(result.Content); text != "" {
				parts = append(parts, "[Tool result]: "+truncateForSummary(text, toolResultMaxChars))
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func serializeAssistant(message protocol.AssistantMessage) []string {
	textParts := make([]string, 0)
	thinkingParts := make([]string, 0)
	toolCalls := make([]string, 0)
	for _, item := range message.Content {
		switch value := item.(type) {
		case protocol.TextContent:
			textParts = append(textParts, value.Text)
		case *protocol.TextContent:
			if value != nil {
				textParts = append(textParts, value.Text)
			}
		case protocol.ThinkingContent:
			thinkingParts = append(thinkingParts, value.Thinking)
		case *protocol.ThinkingContent:
			if value != nil {
				thinkingParts = append(thinkingParts, value.Thinking)
			}
		case protocol.ToolCallContent:
			toolCalls = append(toolCalls, formatToolCall(value))
		case *protocol.ToolCallContent:
			if value != nil {
				toolCalls = append(toolCalls, formatToolCall(*value))
			}
		}
	}
	parts := make([]string, 0, 3)
	if len(thinkingParts) > 0 {
		parts = append(parts, "[Assistant thinking]: "+strings.Join(thinkingParts, "\n"))
	}
	if len(textParts) > 0 {
		parts = append(parts, "[Assistant]: "+strings.Join(textParts, "\n"))
	}
	if len(toolCalls) > 0 {
		parts = append(parts, "[Assistant tool calls]: "+strings.Join(toolCalls, "; "))
	}
	return parts
}

func formatToolCall(call protocol.ToolCallContent) string {
	var arguments map[string]json.RawMessage
	if err := json.Unmarshal(call.Arguments, &arguments); err != nil {
		return fmt.Sprintf("%s(arguments=%s)", call.Name, safeJSONString(call.Arguments))
	}
	keys := make([]string, 0, len(arguments))
	for key := range arguments {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	items := make([]string, 0, len(keys))
	for _, key := range keys {
		items = append(items, key+"="+safeJSONString(arguments[key]))
	}
	return fmt.Sprintf("%s(%s)", call.Name, strings.Join(items, ", "))
}

func safeJSONString(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return "[unserializable]"
	}
	if message, ok := value.(json.RawMessage); ok {
		if !json.Valid(message) {
			return "[unserializable]"
		}
		return string(message)
	}
	return string(raw)
}

func truncateForSummary(text string, maxChars int) string {
	characters := []rune(text)
	if len(characters) <= maxChars {
		return text
	}
	return fmt.Sprintf("%s\n\n[... %d more characters truncated]", string(characters[:maxChars]), len(characters)-maxChars)
}

func textFromContent(content protocol.ContentList) string {
	var text strings.Builder
	for _, item := range content {
		switch value := item.(type) {
		case protocol.TextContent:
			text.WriteString(value.Text)
		case *protocol.TextContent:
			if value != nil {
				text.WriteString(value.Text)
			}
		}
	}
	return text.String()
}

func assistantText(message protocol.AssistantMessage) string {
	parts := make([]string, 0)
	for _, item := range message.Content {
		switch value := item.(type) {
		case protocol.TextContent:
			parts = append(parts, value.Text)
		case *protocol.TextContent:
			if value != nil {
				parts = append(parts, value.Text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func mergeSummaryDetails(operations *FileOperations, details session.SummaryDetails) {
	if operations == nil {
		return
	}
	ensureFileOperations(operations)
	for _, path := range details.ReadFiles {
		addPath(operations.Read, path)
	}
	for _, path := range details.ModifiedFiles {
		addPath(operations.Edited, path)
	}
}

func ensureFileOperations(operations *FileOperations) {
	if operations.Read == nil {
		operations.Read = map[string]struct{}{}
	}
	if operations.Written == nil {
		operations.Written = map[string]struct{}{}
	}
	if operations.Edited == nil {
		operations.Edited = map[string]struct{}{}
	}
}

func addToolPath(operations *FileOperations, toolName, path string) {
	switch toolName {
	case "read":
		addPath(operations.Read, path)
	case "write":
		addPath(operations.Written, path)
	case "edit":
		addPath(operations.Edited, path)
	}
}

func addPath(target map[string]struct{}, path string) {
	path = strings.TrimSpace(path)
	if path != "" {
		target[path] = struct{}{}
	}
}

func pathFromJSON(raw json.RawMessage) string {
	var fields struct {
		Path     string `json:"path"`
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return ""
	}
	if fields.Path != "" {
		return fields.Path
	}
	return fields.FilePath
}

func pathFromDetails(details any) string {
	if details == nil {
		return ""
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return ""
	}
	return pathFromJSON(raw)
}
