package resources

import (
	"context"
	"testing"

	agentcore "oops/internal/agent/core"
	"oops/internal/agent/runtime/skills"
)

func TestCloneCopiesSlices(t *testing.T) {
	snapshot := Snapshot{
		SystemPrompt: "system",
		Tools:        make([]agentcore.Tool, 1),
		ToolNames:    []string{"read"},
		Skills:       []skills.Skill{{Name: "review", Content: "review carefully"}},
	}

	cloned := Clone(snapshot)
	if &cloned.Tools[0] == &snapshot.Tools[0] {
		t.Fatal("tools share backing storage")
	}
	cloned.ToolNames[0] = "write"
	if snapshot.ToolNames[0] != "read" {
		t.Fatalf("source tool name = %q", snapshot.ToolNames[0])
	}
	cloned.Skills[0].Content = "changed"
	if snapshot.Skills[0].Content != "review carefully" {
		t.Fatalf("source skill content = %q", snapshot.Skills[0].Content)
	}
}

func TestStaticLoaderReturnsIndependentSnapshots(t *testing.T) {
	loader := StaticLoader{Snapshot: Snapshot{
		ToolNames: []string{"read"},
		Skills:    []skills.Skill{{Name: "review", Content: "review carefully"}},
	}}
	first, err := loader.Load(context.Background(), Request{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := loader.Load(context.Background(), Request{})
	if err != nil {
		t.Fatal(err)
	}

	first.ToolNames[0] = "write"
	if second.ToolNames[0] != "read" {
		t.Fatalf("second tool name = %q", second.ToolNames[0])
	}
	first.Skills[0].Content = "changed"
	if second.Skills[0].Content != "review carefully" {
		t.Fatalf("second skill content = %q", second.Skills[0].Content)
	}
}
