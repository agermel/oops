package harness

import (
	"context"
	"errors"
	"sync"

	coreagent "oops/internal/llm/core/agent"
	"oops/internal/llm/runtime/session"
)

type RuntimeOptions struct {
	Repo      *session.Repository
	Loader    ResourceLoader
	Config    coreagent.AgentLoopConfig
	CWD       string
	Model     string
	Provider  string
	Reasoning string
}

type NewSessionOptions struct {
	ID              string
	Name            string
	ProjectID       string
	Model           string
	Provider        string
	Reasoning       string
	ActiveToolNames []string
}

type AgentSessionRuntime struct {
	mu sync.Mutex

	repo   *session.Repository
	loader ResourceLoader
	config coreagent.AgentLoopConfig

	cwd       string
	model     string
	provider  string
	reasoning string

	active map[string]*AgentSession
}

func NewRuntime(options RuntimeOptions) *AgentSessionRuntime {
	loader := options.Loader
	if loader == nil {
		loader = StaticResourceLoader{}
	}
	repo := options.Repo
	if repo == nil {
		repo = session.NewRepository(nil)
	}
	return &AgentSessionRuntime{
		repo:      repo,
		loader:    loader,
		config:    options.Config,
		cwd:       options.CWD,
		model:     options.Model,
		provider:  options.Provider,
		reasoning: options.Reasoning,
		active:    map[string]*AgentSession{},
	}
}

func (r *AgentSessionRuntime) NewSession(ctx context.Context, options NewSessionOptions) (*AgentSession, error) {
	r.mu.Lock()
	sess := r.repo.Create(options.ID)
	cwd := r.cwd
	loader := r.loader
	r.mu.Unlock()

	name := options.Name
	entry, err := sess.AppendSessionInfoWithProject(cwd, name, options.ProjectID)
	if err != nil {
		return nil, err
	}
	if err := r.repo.SaveEntry(sess.ID(), entry); err != nil {
		return nil, err
	}
	resources, err := loader.Load(ctx, ResourceRequest{CWD: cwd, SessionID: sess.ID()})
	if err != nil {
		return nil, err
	}
	if options.Model != "" {
		entry, err := sess.AppendModelChange(options.Provider, options.Model)
		if err != nil {
			return nil, err
		}
		if err := r.repo.SaveEntry(sess.ID(), entry); err != nil {
			return nil, err
		}
	}
	if options.Reasoning != "" {
		entry, err := sess.AppendThinkingLevelChange(options.Reasoning)
		if err != nil {
			return nil, err
		}
		if err := r.repo.SaveEntry(sess.ID(), entry); err != nil {
			return nil, err
		}
	}
	if len(options.ActiveToolNames) > 0 {
		if err := validateResourceToolNames(resources.Tools, options.ActiveToolNames); err != nil {
			return nil, err
		}
		entry, err := sess.AppendActiveToolsChange(options.ActiveToolNames)
		if err != nil {
			return nil, err
		}
		if err := r.repo.SaveEntry(sess.ID(), entry); err != nil {
			return nil, err
		}
	}
	return r.attach(ctx, sess, resources, options.Model, options.Provider, options.Reasoning, options.ActiveToolNames)
}

func (r *AgentSessionRuntime) Resume(ctx context.Context, sessionID string) (*AgentSession, error) {
	if sessionID == "" {
		return nil, errors.New("session id is required")
	}
	r.mu.Lock()
	if active := r.active[sessionID]; active != nil {
		r.mu.Unlock()
		return active, nil
	}
	cwd := r.cwd
	loader := r.loader
	r.mu.Unlock()

	sess, err := r.repo.Load(sessionID)
	if err != nil {
		return nil, err
	}
	info := sess.Info()
	if info.CWD != "" {
		cwd = info.CWD
	}
	resources, err := loader.Load(ctx, ResourceRequest{CWD: cwd, SessionID: sess.ID()})
	if err != nil {
		return nil, err
	}
	return r.attach(ctx, sess, resources, "", "", "", nil)
}

func (r *AgentSessionRuntime) attach(ctx context.Context, sess *session.Session, resources ResourceSnapshot, model, provider, reasoning string, activeTools []string) (*AgentSession, error) {
	sessionID := sess.ID()
	r.mu.Lock()
	if active := r.active[sessionID]; active != nil {
		r.mu.Unlock()
		return active, nil
	}
	defaultModel := firstNonEmpty(model, r.model)
	defaultProvider := firstNonEmpty(provider, r.provider)
	defaultReasoning := firstNonEmpty(reasoning, r.reasoning)
	config := r.config
	r.mu.Unlock()
	as, err := NewAgentSession(AgentSessionOptions{
		Session:         sess,
		Repo:            r.repo,
		Resources:       resources,
		Config:          config,
		Model:           defaultModel,
		Provider:        defaultProvider,
		Reasoning:       defaultReasoning,
		ActiveToolNames: activeTools,
	})
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	if active := r.active[sessionID]; active != nil {
		r.mu.Unlock()
		return active, nil
	}
	r.active[sessionID] = as
	r.mu.Unlock()
	_ = ctx
	return as, nil
}
