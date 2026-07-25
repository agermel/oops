package runtime

import (
	"context"
	"errors"
	"sync"

	coreagent "oops/internal/agent/core"
)

type RuntimeOptions struct {
	Repo      *Repository
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
	CWD             string
	Model           string
	Provider        string
	Reasoning       string
	ActiveToolNames []string
	Resources       *ResourceSnapshot
	Config          *coreagent.AgentLoopConfig
}

type ResumeSessionOptions struct {
	CWD             string
	Model           string
	Provider        string
	Reasoning       string
	ActiveToolNames []string
	Resources       *ResourceSnapshot
	Config          *coreagent.AgentLoopConfig
}

var ErrSessionBusy = errors.New("session is busy")

type Runtime struct {
	mu sync.Mutex

	repo   *Repository
	loader ResourceLoader
	config coreagent.AgentLoopConfig

	cwd       string
	model     string
	provider  string
	reasoning string

	active map[string]*AgentSession
	leases map[string]*SessionLease
}

type SessionLease struct {
	runtime   *Runtime
	sessionID string
	once      sync.Once
}

func NewRuntime(options RuntimeOptions) *Runtime {
	loader := options.Loader
	if loader == nil {
		loader = StaticResourceLoader{}
	}
	repo := options.Repo
	if repo == nil {
		repo = NewRepository(nil)
	}
	return &Runtime{
		repo:      repo,
		loader:    loader,
		config:    options.Config,
		cwd:       options.CWD,
		model:     options.Model,
		provider:  options.Provider,
		reasoning: options.Reasoning,
		active:    map[string]*AgentSession{},
		leases:    map[string]*SessionLease{},
	}
}

func (r *Runtime) Repository() *Repository {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.repo
}

func (r *Runtime) AcquireSession(sessionID string) (*SessionLease, error) {
	if sessionID == "" {
		return nil, errors.New("session id is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.leases[sessionID]; exists {
		return nil, ErrSessionBusy
	}
	lease := &SessionLease{runtime: r, sessionID: sessionID}
	r.leases[sessionID] = lease
	return lease, nil
}

func (l *SessionLease) Release() {
	if l == nil || l.runtime == nil {
		return
	}
	l.once.Do(func() {
		l.runtime.mu.Lock()
		if l.runtime.leases[l.sessionID] == l {
			delete(l.runtime.leases, l.sessionID)
		}
		l.runtime.mu.Unlock()
	})
}

func (r *Runtime) NewSession(ctx context.Context, options NewSessionOptions) (*AgentSession, error) {
	r.mu.Lock()
	sess := r.repo.Create(options.ID)
	cwd := firstNonEmpty(options.CWD, r.cwd)
	loader := r.loader
	r.mu.Unlock()

	name := options.Name
	if _, err := r.repo.AppendEntry(sess.ID(), Entry{Type: EntrySessionInfo, CWD: cwd, Name: name, ProjectID: options.ProjectID}); err != nil {
		return nil, err
	}
	resources, err := loadResourceSnapshot(ctx, loader, cwd, sess.ID(), options.Resources)
	if err != nil {
		return nil, err
	}
	if options.Model != "" {
		if _, err := r.repo.AppendEntry(sess.ID(), Entry{Type: EntryModelChange, Provider: options.Provider, Model: options.Model}); err != nil {
			return nil, err
		}
	}
	if options.Reasoning != "" {
		if _, err := r.repo.AppendEntry(sess.ID(), Entry{Type: EntryThinkingLevelChange, Reasoning: options.Reasoning}); err != nil {
			return nil, err
		}
	}
	if len(options.ActiveToolNames) > 0 {
		if err := validateResourceToolNames(resources.Tools, options.ActiveToolNames); err != nil {
			return nil, err
		}
		if _, err := r.repo.AppendEntry(sess.ID(), Entry{Type: EntryActiveToolsChange, ToolNames: options.ActiveToolNames}); err != nil {
			return nil, err
		}
	}
	return r.attach(ctx, sess, resources, options.Config, options.Model, options.Provider, options.Reasoning, options.ActiveToolNames)
}

func (r *Runtime) Resume(ctx context.Context, sessionID string) (*AgentSession, error) {
	return r.ResumeWithOptions(ctx, sessionID, ResumeSessionOptions{})
}

func (r *Runtime) ResumeWithOptions(ctx context.Context, sessionID string, options ResumeSessionOptions) (*AgentSession, error) {
	if sessionID == "" {
		return nil, errors.New("session id is required")
	}
	r.mu.Lock()
	if active := r.active[sessionID]; active != nil {
		r.mu.Unlock()
		return active, nil
	}
	cwd := firstNonEmpty(options.CWD, r.cwd)
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
	resources, err := loadResourceSnapshot(ctx, loader, cwd, sess.ID(), options.Resources)
	if err != nil {
		return nil, err
	}
	return r.attach(ctx, sess, resources, options.Config, options.Model, options.Provider, options.Reasoning, options.ActiveToolNames)
}

func (r *Runtime) attach(ctx context.Context, sess *Session, resources ResourceSnapshot, configOverride *coreagent.AgentLoopConfig, model, provider, reasoning string, activeTools []string) (*AgentSession, error) {
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
	if configOverride != nil {
		config = *configOverride
	}
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

func loadResourceSnapshot(ctx context.Context, loader ResourceLoader, cwd, sessionID string, override *ResourceSnapshot) (ResourceSnapshot, error) {
	if override != nil {
		return cloneResourceSnapshot(*override), nil
	}
	return loader.Load(ctx, ResourceRequest{CWD: cwd, SessionID: sessionID})
}
