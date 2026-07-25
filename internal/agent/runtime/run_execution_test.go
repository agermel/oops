package runtime

import (
	"context"
	"testing"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type namedRunTool struct {
	name string
}

func (t namedRunTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name}, nil
}

func (t namedRunTool) InvokableRun(context.Context, string, ...einotool.Option) (string, error) {
	return "", nil
}

func TestRunToolHelpersPreferReservedNames(t *testing.T) {
	ctx := context.Background()
	unique := uniqueInvokableTools(ctx, []einotool.InvokableTool{
		namedRunTool{name: "read"},
		namedRunTool{name: "read"},
		namedRunTool{name: "repo_read_file"},
	})
	if len(unique) != 2 {
		t.Fatalf("unique len = %d", len(unique))
	}
	enabled := map[string]bool{"read": true, "repo_read_file": true}
	reserved := map[string]bool{"read": true}
	filtered := filterInvokableTools(ctx, unique, enabled, reserved)
	if len(filtered) != 1 {
		t.Fatalf("filtered len = %d", len(filtered))
	}
	info, err := filtered[0].Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "repo_read_file" {
		t.Fatalf("filtered tool = %q", info.Name)
	}
}
