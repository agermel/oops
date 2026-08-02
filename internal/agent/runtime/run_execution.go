package runtime

import (
	"context"
	"errors"
	"sync"

	protocol "oops/internal/agent/ai"
	agentcore "oops/internal/agent/core"
	"oops/internal/agent/runtime/model"
	agentresources "oops/internal/agent/runtime/resources"
	agentskills "oops/internal/agent/runtime/skills"
	agenttools "oops/internal/agent/runtime/tools"
	workspacetools "oops/internal/agent/runtime/tools/workspace"

	einotool "github.com/cloudwego/eino/components/tool"
)

var ErrAgentBusy = agentcore.ErrAgentBusy

type RunEvent interface {
	EventName() string
	runEvent()
}

type CoreAgentEvent protocol.AgentEvent

func (e CoreAgentEvent) EventName() string { return string(e.Type) }
func (CoreAgentEvent) runEvent()           {}

type PreparePromptOptions struct {
	SessionID       string
	NewSessionID    string
	ProjectID       string
	Model           string
	Provider        string
	SystemPrompt    string
	PlatformTools   []einotool.InvokableTool
	MaxTurns        int
	Client          *model.Client
	ActiveToolNames []string
	PromptTemplates []PromptTemplate
	Skills          []agentskills.Skill
}

func (r *Runtime) PreparePromptHarness(ctx context.Context, options PreparePromptOptions) (*AgentHarness, error) {
	if options.Client == nil {
		return nil, errors.New("agent runtime requires model client")
	}
	runtimeTools, err := BuildRunTools(ctx, options.Client, options.PlatformTools)
	if err != nil {
		return nil, err
	}
	requestOptions := options.Client.RequestOptions()
	streamFn, err := options.Client.StreamWithOptions(requestOptions)
	if err != nil {
		return nil, err
	}
	loopConfig := agentcore.AgentLoopConfig{
		MaxTurns: options.MaxTurns,
		Stream:   streamFn,
	}
	resources := agentresources.Snapshot{
		SystemPrompt: options.SystemPrompt,
		Tools:        runtimeTools,
		Skills:       options.Skills,
	}
	providerName := firstNonEmpty(options.Provider, options.Client.Provider(), protocol.DefaultProviderID)
	var harness *AgentHarness
	if options.SessionID != "" {
		harness, err = r.ResumeWithOptions(ctx, options.SessionID, ResumeSessionOptions{
			Resources:       &resources,
			Config:          &loopConfig,
			Model:           options.Model,
			Provider:        providerName,
			ActiveToolNames: options.ActiveToolNames,
			PromptTemplates: options.PromptTemplates,
			ProviderClient:  options.Client,
			RequestOptions:  &requestOptions,
		})
	} else {
		harness, err = r.NewSession(ctx, NewSessionOptions{
			ID:              options.NewSessionID,
			Model:           options.Model,
			Provider:        providerName,
			ProjectID:       options.ProjectID,
			Resources:       &resources,
			Config:          &loopConfig,
			ActiveToolNames: options.ActiveToolNames,
			PromptTemplates: options.PromptTemplates,
			ProviderClient:  options.Client,
			RequestOptions:  &requestOptions,
		})
	}
	if err != nil {
		return nil, err
	}
	return harness, nil
}

func (s *AgentHarness) ListenRunEvents(listener func(context.Context, RunEvent) error) func() {
	if listener == nil {
		return func() {}
	}
	s.mu.Lock()
	id := s.nextRunListenerID
	s.nextRunListenerID++
	s.runListeners[id] = listener
	s.runListenerOrder = append(s.runListenerOrder, id)
	s.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.runListeners, id)
			for i, value := range s.runListenerOrder {
				if value == id {
					s.runListenerOrder = append(s.runListenerOrder[:i], s.runListenerOrder[i+1:]...)
					break
				}
			}
			s.mu.Unlock()
		})
	}
}

func (s *AgentHarness) PromptText(ctx context.Context, text string, timestamp int64) error {
	s.mu.Lock()
	resolved, err := ResolvePromptCommand(text, s.promptTemplates, s.resources.Skills)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	_, err = s.promptLocked(ctx, protocol.MessageList{protocol.UserMessage{
		Content:   protocol.ContentList{protocol.NewTextContent(resolved)},
		Timestamp: timestamp,
	}})
	return err
}

func BuildRunTools(ctx context.Context, client *model.Client, platformTools []einotool.InvokableTool) ([]agentcore.Tool, error) {
	if client == nil {
		return nil, errors.New("agent runtime requires model client")
	}
	workspaceRuntimeTools, err := workspacetools.NewTools(workspacetools.Options{})
	if err != nil {
		return nil, err
	}
	workspaceModelTools, err := agenttools.ToInvokableTools(workspaceRuntimeTools)
	if err != nil {
		return nil, err
	}
	workspaceNames := runtimeToolNameSet(workspaceRuntimeTools)
	allModelTools := make([]einotool.InvokableTool, 0, len(platformTools)+len(workspaceModelTools))
	allModelTools = append(allModelTools, workspaceModelTools...)
	allModelTools = append(allModelTools, platformTools...)
	allModelTools = uniqueInvokableTools(ctx, allModelTools)
	enabledModelTools := client.EnabledTools(allModelTools)
	enabledNames := invokableToolNameSet(ctx, enabledModelTools)

	enabledPlatformTools := filterInvokableTools(ctx, platformTools, enabledNames, workspaceNames)
	runtimeTools, _, err := agenttools.FromInvokableTools(ctx, enabledPlatformTools)
	if err != nil {
		return nil, err
	}
	for _, item := range workspaceRuntimeTools {
		if item == nil {
			continue
		}
		if enabledNames[item.Definition().Name] {
			runtimeTools = append(runtimeTools, item)
		}
	}
	return runtimeTools, nil
}

func runtimeToolNameSet(tools []agentcore.Tool) map[string]bool {
	names := make(map[string]bool, len(tools))
	for _, item := range tools {
		if item == nil {
			continue
		}
		if name := item.Definition().Name; name != "" {
			names[name] = true
		}
	}
	return names
}

func invokableToolNameSet(ctx context.Context, tools []einotool.InvokableTool) map[string]bool {
	names := make(map[string]bool, len(tools))
	for _, item := range tools {
		if item == nil {
			continue
		}
		info, err := item.Info(ctx)
		if err != nil || info == nil || info.Name == "" {
			continue
		}
		names[info.Name] = true
	}
	return names
}

func uniqueInvokableTools(ctx context.Context, tools []einotool.InvokableTool) []einotool.InvokableTool {
	if len(tools) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]einotool.InvokableTool, 0, len(tools))
	for _, item := range tools {
		if item == nil {
			continue
		}
		info, err := item.Info(ctx)
		if err != nil || info == nil || info.Name == "" {
			continue
		}
		if seen[info.Name] {
			continue
		}
		seen[info.Name] = true
		out = append(out, item)
	}
	return out
}

func filterInvokableTools(ctx context.Context, tools []einotool.InvokableTool, enabled, reserved map[string]bool) []einotool.InvokableTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]einotool.InvokableTool, 0, len(tools))
	for _, item := range tools {
		if item == nil {
			continue
		}
		info, err := item.Info(ctx)
		if err != nil || info == nil {
			continue
		}
		if reserved[info.Name] {
			continue
		}
		if enabled[info.Name] {
			out = append(out, item)
		}
	}
	return out
}
