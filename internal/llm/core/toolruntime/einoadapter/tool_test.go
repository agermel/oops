package einoadapter

import (
	"context"
	"encoding/json"
	"testing"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"oops/internal/llm/ai/protocol"
	"oops/internal/llm/core/toolruntime"
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
