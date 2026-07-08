package toolruntime

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"oops/internal/llm/ai/protocol"
)

type Registry struct {
	order []string
	tools map[string]registeredTool
}

type registeredTool struct {
	tool       Tool
	definition protocol.ToolDefinition
	schema     *jsonschema.Schema
}

func NewRegistry(tools []Tool) (*Registry, error) {
	registry := &Registry{tools: map[string]registeredTool{}}
	for _, tool := range tools {
		if err := registry.Register(tool); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func (r *Registry) Register(tool Tool) error {
	if tool == nil {
		return errors.New("tool is nil")
	}
	if r.tools == nil {
		r.tools = map[string]registeredTool{}
	}
	definition := protocol.CloneTools([]protocol.ToolDefinition{tool.Definition()})[0]
	if definition.Name == "" {
		return errors.New("tool definition requires name")
	}
	if _, exists := r.tools[definition.Name]; exists {
		return fmt.Errorf("duplicate tool %q", definition.Name)
	}
	schema, err := compileSchema(definition.Parameters)
	if err != nil {
		return fmt.Errorf("compile schema for %q: %w", definition.Name, err)
	}
	r.order = append(r.order, definition.Name)
	r.tools[definition.Name] = registeredTool{tool: tool, definition: definition, schema: schema}
	return nil
}

func (r *Registry) Lookup(name string) (Tool, bool) {
	item, ok := r.lookup(name)
	if !ok {
		return nil, false
	}
	return item.tool, true
}

func (r *Registry) Definitions() []protocol.ToolDefinition {
	if r == nil || len(r.order) == 0 {
		return nil
	}
	defs := make([]protocol.ToolDefinition, 0, len(r.order))
	for _, name := range r.order {
		defs = append(defs, r.tools[name].definition)
	}
	return protocol.CloneTools(defs)
}

func (r *Registry) lookup(name string) (registeredTool, bool) {
	if r == nil {
		return registeredTool{}, false
	}
	item, ok := r.tools[name]
	return item, ok
}

func compileSchema(raw json.RawMessage) (*jsonschema.Schema, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("schema.json", doc); err != nil {
		return nil, err
	}
	return compiler.Compile("schema.json")
}

func validateWithSchema(schema *jsonschema.Schema, args map[string]any) error {
	if schema == nil {
		return nil
	}
	return schema.Validate(args)
}
