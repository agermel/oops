package harness

import (
	"context"
	"errors"
	"fmt"

	"oops/internal/llm/ai/protocol"
	"oops/internal/llm/core/toolruntime"
)

type ResourceRequest struct {
	CWD       string
	SessionID string
}

type ResourceSnapshot struct {
	SystemPrompt string
	Tools        []toolruntime.Tool
	ToolNames    []string
}

type ResourceLoader interface {
	Load(context.Context, ResourceRequest) (ResourceSnapshot, error)
}

type StaticResourceLoader struct {
	Snapshot ResourceSnapshot
}

func (l StaticResourceLoader) Load(context.Context, ResourceRequest) (ResourceSnapshot, error) {
	return cloneResourceSnapshot(l.Snapshot), nil
}

func cloneResourceSnapshot(snapshot ResourceSnapshot) ResourceSnapshot {
	out := ResourceSnapshot{
		SystemPrompt: snapshot.SystemPrompt,
		Tools:        cloneTools(snapshot.Tools),
		ToolNames:    cloneStrings(snapshot.ToolNames),
	}
	return out
}

func cloneTools(tools []toolruntime.Tool) []toolruntime.Tool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]toolruntime.Tool, len(tools))
	copy(out, tools)
	return out
}

func cloneDefinitions(defs []protocol.ToolDefinition) []protocol.ToolDefinition {
	return protocol.CloneTools(defs)
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}

func validateResourceToolNames(tools []toolruntime.Tool, names []string) error {
	known := map[string]bool{}
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		name := tool.Definition().Name
		if name != "" {
			known[name] = true
		}
	}
	seen := map[string]bool{}
	for _, name := range names {
		if name == "" {
			return errors.New("active tool name is required")
		}
		if seen[name] {
			return fmt.Errorf("duplicate active tool %q", name)
		}
		if !known[name] {
			return fmt.Errorf("unknown active tool %q", name)
		}
		seen[name] = true
	}
	return nil
}
