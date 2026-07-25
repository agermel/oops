package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	protocol "oops/internal/agent/ai"
	toolruntime "oops/internal/agent/core"
)

type fakeInvokable struct {
	info     *schema.ToolInfo
	argsJSON string
}

func (f *fakeInvokable) Info(context.Context) (*schema.ToolInfo, error) {
	return f.info, nil
}

func (f *fakeInvokable) InvokableRun(_ context.Context, argumentsInJSON string, _ ...einotool.Option) (string, error) {
	f.argsJSON = argumentsInJSON
	return "ok", nil
}

func TestFromInvokableToolPreservesInfoNameAndSchema(t *testing.T) {
	invokable := &fakeInvokable{
		info: &schema.ToolInfo{
			Name: "model_visible_name",
			Desc: "lookup data",
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"query": {Type: schema.String, Required: true},
			}),
		},
	}
	tool, definition, err := FromInvokableTool(context.Background(), invokable)
	if err != nil {
		t.Fatalf("FromInvokableTool() error = %v", err)
	}
	if definition.Name != "model_visible_name" || tool.Definition().Name != "model_visible_name" {
		t.Fatalf("definition name = %q / %q", definition.Name, tool.Definition().Name)
	}
	if !json.Valid(definition.Parameters) || len(definition.Parameters) == 0 {
		t.Fatalf("parameters = %s, want valid schema", definition.Parameters)
	}
}

func TestToolExecutePassesRawArgumentsAndWrapsString(t *testing.T) {
	invokable := &fakeInvokable{info: &schema.ToolInfo{Name: "lookup"}}
	tool, _, err := FromInvokableTool(context.Background(), invokable)
	if err != nil {
		t.Fatalf("FromInvokableTool() error = %v", err)
	}
	result, err := tool.Execute(context.Background(), toolruntime.ToolCall{RawArguments: json.RawMessage(`{"query":"pods"}`)}, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if invokable.argsJSON != `{"query":"pods"}` {
		t.Fatalf("args = %s", invokable.argsJSON)
	}
	text, ok := result.Content[0].(protocol.TextContent)
	if !ok || text.Text != "ok" {
		t.Fatalf("content = %#v, want text ok", result.Content[0])
	}
}

func TestToInvokableToolExposesRuntimeDefinitionAndRunsTool(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	runtimeTools, err := NewWorkspaceTools(Options{Root: root})
	if err != nil {
		t.Fatalf("NewWorkspaceTools() error = %v", err)
	}
	invokable, err := ToInvokableTool(runtimeTools[0])
	if err != nil {
		t.Fatalf("ToInvokableTool() error = %v", err)
	}
	info, err := invokable.Info(context.Background())
	if err != nil {
		t.Fatalf("Info() error = %v", err)
	}
	if info.Name != "read" || info.ParamsOneOf == nil {
		t.Fatalf("info = %#v", info)
	}
	js, err := info.ParamsOneOf.ToJSONSchema()
	if err != nil {
		t.Fatalf("ToJSONSchema() error = %v", err)
	}
	if js == nil {
		t.Fatal("json schema is nil")
	}
	out, err := invokable.InvokableRun(context.Background(), `{"path":"notes.txt"}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("out = %q", out)
	}
}
