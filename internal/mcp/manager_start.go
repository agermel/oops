package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"oops/internal/config"
	"oops/internal/logutil"

	"github.com/cenkalti/backoff/v4"
	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"
)

func (m *Manager) scheduleStartLocked(id string) connectionStart {
	if current, ok := m.starting[id]; ok && current.cancel != nil {
		current.cancel()
	}
	m.nextStart++
	token := m.nextStart
	ctx, cancel := context.WithCancel(m.lifecycleContext())
	m.starting[id] = scheduledStart{token: token, cancel: cancel}
	delete(m.errors, id)
	return connectionStart{token: token, ctx: ctx}
}

func (m *Manager) launchStart(start connectionStart) {
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		m.startAsync(start)
	}()
}

func (m *Manager) startAsync(start connectionStart) {
	cfg, token := start.cfg, start.token
	hub := NewConnectionLogHub(cfg.ID)
	m.mu.Lock()
	if !m.closed {
		if _, exists := m.connectionLocked(cfg.ID); exists {
			hub = m.ensureLogHubLocked(cfg.ID)
		}
	}
	startProc := m.startProc
	m.mu.Unlock()
	hub.Append("system", "info", fmt.Sprintf("starting connection %q", cfg.Name))
	if startProc == nil {
		startProc = startProcess
	}
	proc, err := startProc(start.ctx, cfg, hub)
	if err == nil && proc != nil {
		if prepareErr := proc.prepare(start.ctx, m.lifecycleContext(), cfg); prepareErr != nil {
			proc.close()
			proc = nil
			err = prepareErr
		}
	}

	m.mutationMu.Lock()
	m.mu.Lock()
	current, isStarting := m.starting[cfg.ID]
	if !isStarting || current.token != token {
		m.mu.Unlock()
		m.mutationMu.Unlock()
		if proc != nil {
			proc.close()
		}
		hub.Append("system", "warn", "start cancelled by newer change")
		return
	}

	currentCfg, exists := m.connectionLocked(cfg.ID)
	if m.closed || !exists || !currentCfg.Enabled || !sameRuntimeConfig(currentCfg, cfg) {
		delete(m.starting, cfg.ID)
		if current.cancel != nil {
			current.cancel()
		}
		m.mu.Unlock()
		m.mutationMu.Unlock()
		if proc != nil {
			proc.close()
		}
		hub.Append("system", "warn", "start cancelled because connection config changed")
		return
	}

	delete(m.starting, cfg.ID)
	if current.cancel != nil {
		current.cancel()
	}
	if err != nil {
		logutil.Error("mcp: start", zap.String("id", cfg.ID), zap.Error(err))
		m.errors[cfg.ID] = err.Error()
		m.mu.Unlock()
		m.mutationMu.Unlock()
		hub.Append("system", "error", fmt.Sprintf("connection start error: %v", err))
		return
	}
	if proc == nil {
		logutil.Error("mcp: start", zap.String("id", cfg.ID), zap.Error(errors.New("start returned nil process")))
		m.errors[cfg.ID] = "start returned nil process"
		m.mu.Unlock()
		m.mutationMu.Unlock()
		hub.Append("system", "error", "connection start error: start returned nil process")
		return
	}
	if _, exists := m.processes[cfg.ID]; exists {
		m.mu.Unlock()
		m.mutationMu.Unlock()
		proc.close()
		hub.Append("system", "warn", "start cancelled because connection is already running")
		return
	}

	m.processes[cfg.ID] = proc
	delete(m.errors, cfg.ID)
	change := m.refreshVisibleToolsLocked()
	logutil.Info("mcp: started",
		zap.String("id", cfg.ID),
		zap.Int("tools", len(proc.metadata)),
	)
	m.mu.Unlock()
	m.enqueueToolChange(change)
	m.startHealthCheck(proc)
	m.mutationMu.Unlock()
	hub.Append("system", "info", fmt.Sprintf("connection started with %d tools", len(proc.metadata)))
}

func startProcess(ctx context.Context, cfg ConnectionConfig, hub *ConnectionLogHub) (*managedProcess, error) {
	transport := cfg.Transport
	if transport == "" {
		transport = "stdio"
	}
	mcpCfg := config.MCPConfig{
		Enabled:   true,
		Transport: transport,
		Command:   cfg.Command,
		Args:      expandEnvSlice(cfg.Args),
		Env:       expandEnvSlice(cfg.Env),
		URL:       cfg.URL,
	}

	const maxAttempts = 3
	var process *managedProcess
	attempt := 0
	err := backoff.Retry(func() error {
		if err := ctx.Err(); err != nil {
			return backoff.Permanent(err)
		}

		attempt++
		attemptCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		if hub != nil {
			hub.Append("system", "info", fmt.Sprintf("connect attempt %d/%d via %s", attempt, maxAttempts, transport))
		}

		var logWriter io.Writer
		if hub != nil {
			logWriter = hub.LineWriter(connectionLogStream(transport))
		}
		session, tools, instructions, closer, err := ConnectWithLog(attemptCtx, mcpCfg, logWriter)
		var metadata []managedTool
		if err == nil {
			metadata, err = collectManagedTools(attemptCtx, tools)
		}
		if err == nil {
			names := make([]string, 0, len(metadata))
			for _, item := range metadata {
				names = append(names, item.info.Name)
			}
			if verifyErr := verifyBackend(attemptCtx, cfg, session, names); verifyErr != nil {
				err = fmt.Errorf("backend verify: %w", verifyErr)
			}
		}
		var prompts []mcp.Prompt
		var resources []mcp.Resource
		if err == nil {
			prompts, resources = discoverPromptsAndResources(attemptCtx, session, hub)
		}
		if err != nil && closer != nil {
			closer()
			closer = nil
		}
		cancel()

		if err == nil {
			if hub != nil {
				hub.Append("system", "info", fmt.Sprintf("connect attempt %d/%d passed", attempt, maxAttempts))
			}
			process = &managedProcess{
				cfg:          cfg,
				session:      session,
				closer:       closer,
				tools:        tools,
				metadata:     metadata,
				instructions: instructions,
				prompts:      prompts,
				resources:    resources,
			}
			return nil
		}

		if hub != nil {
			level := "warn"
			if attempt == maxAttempts {
				level = "error"
			}
			hub.Append("system", level, fmt.Sprintf("connect attempt %d/%d error: %v", attempt, maxAttempts, err))
		}
		if ctx.Err() != nil {
			return backoff.Permanent(ctx.Err())
		}
		if isPermanentStartError(err) {
			return backoff.Permanent(err)
		}
		logutil.Warn("mcp: connect retry",
			zap.String("id", cfg.ID),
			zap.Int("attempt", attempt),
			zap.Int("max", maxAttempts),
			zap.Error(err),
		)
		return err
	}, backoff.WithContext(backoff.WithMaxRetries(backoff.NewConstantBackOff(2*time.Second), maxAttempts-1), ctx))
	if err != nil {
		return nil, fmt.Errorf("connect (%d attempts): %w", maxAttempts, err)
	}
	return process, nil
}

// discoverPromptsAndResources 尽力拉取 server 暴露的 prompts 与 resources 列表。
// 许多 server 并未实现 prompts/resources capability，对应调用会返回错误；这类失败
// 不影响连接建立，降级为空列表并记录日志即可。
func discoverPromptsAndResources(ctx context.Context, session MCPSession, hub *ConnectionLogHub) ([]mcp.Prompt, []mcp.Resource) {
	var prompts []mcp.Prompt
	if res, err := session.ListPrompts(ctx, mcp.ListPromptsRequest{}); err != nil {
		if hub != nil {
			hub.Append("system", "warn", fmt.Sprintf("prompts/list unavailable: %v", err))
		}
	} else {
		prompts = res.Prompts
	}

	var resources []mcp.Resource
	if res, err := session.ListResources(ctx, mcp.ListResourcesRequest{}); err != nil {
		if hub != nil {
			hub.Append("system", "warn", fmt.Sprintf("resources/list unavailable: %v", err))
		}
	} else {
		resources = res.Resources
	}

	return prompts, resources
}

func isPermanentStartError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var statusError interface{ StatusCode() int }
	if errors.As(err, &statusError) {
		status := statusError.StatusCode()
		if status >= http.StatusBadRequest && status < http.StatusInternalServerError {
			return true
		}
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "unauthorized") ||
		strings.Contains(message, "forbidden") ||
		strings.Contains(message, "authentication failed") {
		return true
	}
	for status := http.StatusBadRequest; status < http.StatusInternalServerError; status++ {
		code := fmt.Sprintf("%d", status)
		statusText := strings.ToLower(http.StatusText(status))
		if strings.Contains(message, "status "+code) ||
			strings.Contains(message, "status code "+code) ||
			strings.Contains(message, "http "+code) ||
			(statusText != "" && strings.Contains(message, statusText)) {
			return true
		}
	}
	return false
}
