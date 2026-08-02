package resources

import (
	"context"
	"slices"

	agentcore "oops/internal/agent/core"
	"oops/internal/agent/runtime/skills"
)

type Request struct {
	CWD       string
	SessionID string
}

type Snapshot struct {
	SystemPrompt string
	Tools        []agentcore.Tool
	ToolNames    []string
	Skills       []skills.Skill
}

type Loader interface {
	Load(context.Context, Request) (Snapshot, error)
}

type StaticLoader struct {
	Snapshot Snapshot
}

func (l StaticLoader) Load(context.Context, Request) (Snapshot, error) {
	return Clone(l.Snapshot), nil
}

func Clone(snapshot Snapshot) Snapshot {
	return Snapshot{
		SystemPrompt: snapshot.SystemPrompt,
		Tools:        slices.Clone(snapshot.Tools),
		ToolNames:    slices.Clone(snapshot.ToolNames),
		Skills:       slices.Clone(snapshot.Skills),
	}
}
