package runtime

import (
	"context"
	"errors"

	protocol "oops/internal/agent/ai"
	agentcore "oops/internal/agent/core"

	einotool "github.com/cloudwego/eino/components/tool"
)

var ErrAgentBusy = agentcore.ErrAgentBusy

type RunEvent struct {
	Name    string
	Payload any
}

type PreparePromptOptions struct {
	SessionID       string
	NewSessionID    string
	ProjectID       string
	Model           string
	Provider        string
	SystemPrompt    string
	PlatformTools   []einotool.InvokableTool
	MaxTurns        int
	Client          *Client
	ActiveToolNames []string
}

func (r *Runtime) PreparePromptSession(ctx context.Context, options PreparePromptOptions) (*AgentSession, error) {
	if options.Client == nil {
		return nil, errors.New("agent runtime requires model client")
	}
	runtimeTools, err := BuildRunTools(ctx, options.Client, options.PlatformTools)
	if err != nil {
		return nil, err
	}
	streamFn := options.Client.Stream()
	if streamFn == nil {
		return nil, errors.New("agent runtime requires model stream")
	}
	loopConfig := agentcore.AgentLoopConfig{
		MaxTurns: options.MaxTurns,
		Stream:   streamFn,
	}
	resources := ResourceSnapshot{
		SystemPrompt: options.SystemPrompt,
		Tools:        runtimeTools,
	}
	providerName := firstNonEmpty(options.Provider, options.Client.Provider(), protocol.DefaultProviderID)
	if options.SessionID != "" {
		return r.ResumeWithOptions(ctx, options.SessionID, ResumeSessionOptions{
			Resources:       &resources,
			Config:          &loopConfig,
			Model:           options.Model,
			Provider:        providerName,
			ActiveToolNames: options.ActiveToolNames,
		})
	}
	return r.NewSession(ctx, NewSessionOptions{
		ID:              options.NewSessionID,
		Model:           options.Model,
		Provider:        providerName,
		ProjectID:       options.ProjectID,
		Resources:       &resources,
		Config:          &loopConfig,
		ActiveToolNames: options.ActiveToolNames,
	})
}

func (s *AgentSession) ListenRunEvents(listener func(context.Context, RunEvent) error) func() {
	if listener == nil {
		return func() {}
	}
	return s.Listen(func(ctx context.Context, event protocol.AgentEvent, _ agentcore.AgentState) error {
		return listener(ctx, RunEvent{
			Name:    string(event.Type),
			Payload: event,
		})
	})
}

func (s *AgentSession) PromptText(ctx context.Context, text string, timestamp int64) error {
	_, err := s.Prompt(ctx, protocol.MessageList{protocol.UserMessage{
		Content:   protocol.ContentList{protocol.NewTextContent(text)},
		Timestamp: timestamp,
	}})
	return err
}

func BuildRunTools(ctx context.Context, client *Client, platformTools []einotool.InvokableTool) ([]agentcore.Tool, error) {
	if client == nil {
		return nil, errors.New("agent runtime requires model client")
	}
	workspaceRuntimeTools, err := NewWorkspaceTools(Options{})
	if err != nil {
		return nil, err
	}
	workspaceModelTools, err := ToInvokableTools(workspaceRuntimeTools)
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
	runtimeTools, _, err := FromInvokableTools(ctx, enabledPlatformTools)
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
