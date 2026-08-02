package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"

	coreagent "oops/internal/agent/core"
	"oops/internal/agent/runtime/model"
	agentresources "oops/internal/agent/runtime/resources"
	"oops/internal/agent/runtime/session"
)

type RuntimeOptions struct {
	Repo             *session.Repository
	Loader           agentresources.Loader
	Config           coreagent.AgentLoopConfig
	CWD              string
	Model            string
	Provider         string
	Reasoning        string
	PromptTemplates  []PromptTemplate
	BeforeAgentStart BeforeAgentStartHook
}

type NewSessionOptions struct {
	ID               string
	Name             string
	ProjectID        string
	CWD              string
	Model            string
	Provider         string
	Reasoning        string
	ActiveToolNames  []string
	PromptTemplates  []PromptTemplate
	Resources        *agentresources.Snapshot
	Config           *coreagent.AgentLoopConfig
	ProviderClient   *model.Client
	RequestOptions   *model.RequestOptions
	BeforeAgentStart BeforeAgentStartHook
}

type ResumeSessionOptions struct {
	CWD              string
	Model            string
	Provider         string
	Reasoning        string
	ActiveToolNames  []string
	PromptTemplates  []PromptTemplate
	Resources        *agentresources.Snapshot
	Config           *coreagent.AgentLoopConfig
	ProviderClient   *model.Client
	RequestOptions   *model.RequestOptions
	BeforeAgentStart BeforeAgentStartHook
}

var ErrSessionBusy = errors.New("session is busy")

type Runtime struct {
	mu sync.Mutex

	repo   *session.Repository
	loader agentresources.Loader
	config coreagent.AgentLoopConfig

	cwd              string
	model            string
	provider         string
	reasoning        string
	promptTemplates  []PromptTemplate
	beforeAgentStart BeforeAgentStartHook

	active   map[string]*AgentHarness
	leases   map[string]*SessionLease
	creating map[string]struct{}
}

type SessionLease struct {
	runtime   *Runtime
	sessionID string
	once      sync.Once
}

func NewRuntime(options RuntimeOptions) *Runtime {
	loader := options.Loader
	if loader == nil {
		loader = agentresources.StaticLoader{}
	}
	repo := options.Repo
	if repo == nil {
		repo = session.NewRepository(nil)
	}
	return &Runtime{
		repo:             repo,
		loader:           loader,
		config:           options.Config,
		cwd:              options.CWD,
		model:            options.Model,
		provider:         options.Provider,
		reasoning:        options.Reasoning,
		promptTemplates:  clonePromptTemplates(options.PromptTemplates),
		beforeAgentStart: options.BeforeAgentStart,
		active:           map[string]*AgentHarness{},
		leases:           map[string]*SessionLease{},
		creating:         map[string]struct{}{},
	}
}

func (r *Runtime) Repository() *session.Repository {
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

func (r *Runtime) NewSession(ctx context.Context, options NewSessionOptions) (*AgentHarness, error) {
	sess := session.New(options.ID)
	sessionID := sess.ID()
	r.mu.Lock()
	if _, exists := r.creating[sessionID]; exists {
		r.mu.Unlock()
		return nil, fmt.Errorf("%w: %q", session.ErrSessionExists, sessionID)
	}
	if _, exists := r.active[sessionID]; exists {
		r.mu.Unlock()
		return nil, fmt.Errorf("%w: %q", session.ErrSessionExists, sessionID)
	}
	r.creating[sessionID] = struct{}{}
	cwd := firstNonEmpty(options.CWD, r.cwd)
	loader := r.loader
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.creating, sessionID)
		r.mu.Unlock()
	}()

	resources, err := loadResourceSnapshot(ctx, loader, cwd, sessionID, options.Resources)
	if err != nil {
		return nil, err
	}
	if options.ActiveToolNames != nil {
		if err := validateResourceToolNames(resources.Tools, options.ActiveToolNames); err != nil {
			return nil, err
		}
	}
	if _, err := sess.AppendSessionInfoWithProject(cwd, options.Name, options.ProjectID); err != nil {
		return nil, err
	}
	if options.Model != "" {
		if _, err := sess.AppendModelChange(options.Provider, options.Model); err != nil {
			return nil, err
		}
	}
	if options.Reasoning != "" {
		if _, err := sess.AppendThinkingLevelChange(options.Reasoning); err != nil {
			return nil, err
		}
	}
	if options.ActiveToolNames != nil {
		if _, err := sess.AppendActiveToolsChange(options.ActiveToolNames); err != nil {
			return nil, err
		}
	}
	return r.attach(ctx, sess, resources, options.Config, options.Model, options.Provider, options.Reasoning, options.ActiveToolNames, options.PromptTemplates, options.ProviderClient, options.RequestOptions, options.BeforeAgentStart, true)
}

// DiscardSession removes the exact active harness and its repository session.
func (r *Runtime) DiscardSession(harness *AgentHarness) error {
	if harness == nil || harness.session == nil {
		return errors.New("agent harness is required")
	}
	sessionID := harness.session.ID()
	r.mu.Lock()
	if _, creating := r.creating[sessionID]; creating {
		r.mu.Unlock()
		return ErrSessionBusy
	}
	if r.active[sessionID] != harness {
		r.mu.Unlock()
		return errors.New("agent harness is not active in runtime")
	}
	r.creating[sessionID] = struct{}{}
	delete(r.active, sessionID)
	repo := r.repo
	r.mu.Unlock()

	deleted, err := repo.DeleteSession(harness.session)
	r.mu.Lock()
	delete(r.creating, sessionID)
	if err != nil && !errors.Is(err, session.ErrSessionMismatch) {
		r.active[sessionID] = harness
	}
	r.mu.Unlock()
	if err == nil && !deleted {
		return fmt.Errorf("session %q was not deleted", sessionID)
	}
	return err
}

func (r *Runtime) Resume(ctx context.Context, sessionID string) (*AgentHarness, error) {
	return r.ResumeWithOptions(ctx, sessionID, ResumeSessionOptions{})
}

func (r *Runtime) ResumeWithOptions(ctx context.Context, sessionID string, options ResumeSessionOptions) (*AgentHarness, error) {
	if sessionID == "" {
		return nil, errors.New("session id is required")
	}
	r.mu.Lock()
	if _, creating := r.creating[sessionID]; creating {
		r.mu.Unlock()
		return nil, ErrSessionBusy
	}
	cwd := firstNonEmpty(options.CWD, r.cwd)
	loader := r.loader
	repo := r.repo
	r.mu.Unlock()

	sess, err := repo.Load(sessionID)
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
	return r.attach(ctx, sess, resources, options.Config, options.Model, options.Provider, options.Reasoning, options.ActiveToolNames, options.PromptTemplates, options.ProviderClient, options.RequestOptions, options.BeforeAgentStart, false)
}

func (r *Runtime) attach(ctx context.Context, sess *session.Session, resources agentresources.Snapshot, configOverride *coreagent.AgentLoopConfig, model, provider, reasoning string, activeTools []string, promptTemplates []PromptTemplate, providerClient *model.Client, requestOptions *model.RequestOptions, beforeAgentStart BeforeAgentStartHook, createSession bool) (*AgentHarness, error) {
	sessionID := sess.ID()
	r.mu.Lock()
	active := r.active[sessionID]
	repo := r.repo
	defaultModel := firstNonEmpty(model, r.model)
	defaultProvider := firstNonEmpty(provider, r.provider)
	defaultReasoning := firstNonEmpty(reasoning, r.reasoning)
	defaultPromptTemplates := clonePromptTemplates(r.promptTemplates)
	if promptTemplates != nil {
		defaultPromptTemplates = promptTemplates
	}
	defaultBeforeAgentStart := r.beforeAgentStart
	if beforeAgentStart != nil {
		defaultBeforeAgentStart = beforeAgentStart
	}
	config := r.config
	if configOverride != nil {
		config = *configOverride
	}
	r.mu.Unlock()
	harnessOptions := AgentHarnessOptions{
		Session:          sess,
		Repo:             repo,
		Resources:        resources,
		Config:           config,
		ProviderClient:   providerClient,
		RequestOptions:   requestOptions,
		Model:            defaultModel,
		Provider:         defaultProvider,
		Reasoning:        defaultReasoning,
		ActiveToolNames:  activeTools,
		PromptTemplates:  defaultPromptTemplates,
		BeforeAgentStart: defaultBeforeAgentStart,
	}
	if active != nil {
		if createSession {
			return nil, fmt.Errorf("%w: %q", session.ErrSessionExists, sessionID)
		}
		if err := active.refresh(harnessOptions); err != nil {
			return nil, err
		}
		return active, nil
	}
	as, err := newAgentHarness(harnessOptions, false)
	if err != nil {
		return nil, err
	}
	if createSession {
		if err := repo.CreateSession(sess); err != nil {
			return nil, err
		}
		r.mu.Lock()
		r.active[sessionID] = as
		r.mu.Unlock()
		_ = ctx
		return as, nil
	}
	r.mu.Lock()
	if active = r.active[sessionID]; active != nil {
		r.mu.Unlock()
		if err := active.refresh(harnessOptions); err != nil {
			return nil, err
		}
		return active, nil
	}
	r.active[sessionID] = as
	r.mu.Unlock()
	_ = ctx
	return as, nil
}

func loadResourceSnapshot(ctx context.Context, loader agentresources.Loader, cwd, sessionID string, override *agentresources.Snapshot) (agentresources.Snapshot, error) {
	if override != nil {
		return agentresources.Clone(*override), nil
	}
	return loader.Load(ctx, agentresources.Request{CWD: cwd, SessionID: sessionID})
}

func validateResourceToolNames(tools []coreagent.Tool, names []string) error {
	known := map[string]bool{}
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		name := tool.Definition().Name
		if name != "" {
			known[name] = true
		}
	}
	seen := map[string]bool{}
	for _, name := range names {
		if name == "" {
			return errors.New("active tool name is required")
		}
		if seen[name] {
			return fmt.Errorf("duplicate active tool %q", name)
		}
		if !known[name] {
			return fmt.Errorf("unknown active tool %q", name)
		}
		seen[name] = true
	}
	return nil
}
