package mcp

import (
	"fmt"
	"slices"
	"strings"

	"oops/internal/logutil"
	runtimestore "oops/internal/store/runtime"

	"go.uber.org/zap"
)

func (m *Manager) load() error {
	ctx := m.lifecycleContext()
	records, err := m.runtime.ListMCPConnections(ctx)
	if err != nil {
		return err
	}
	m.config.Connections = make([]ConnectionConfig, 0, len(records))
	for _, r := range records {
		m.config.Connections = append(m.config.Connections, mcpConnectionFromRuntime(r))
	}
	if m.config.Connections == nil {
		m.config.Connections = []ConnectionConfig{}
	}
	normalized := false
	for i := range m.config.Connections {
		next := normalizeConnectionConfig(m.config.Connections[i])
		if strings.TrimSpace(next.NodeletID) == "" && next.Enabled {
			next.Enabled = false
			logutil.Warn("mcp: disabled unbound connection pending server binding",
				zap.String("id", next.ID),
				zap.String("name", next.Name),
			)
		}
		if !sameStoredConnectionConfig(m.config.Connections[i], next) {
			m.config.Connections[i] = next
			normalized = true
		}
	}
	if normalized {
		return m.save(m.config)
	}
	return nil
}

func (m *Manager) save(state managerState) error {
	return m.runtime.ReplaceMCPConnections(m.lifecycleContext(), mcpConnectionsToRuntime(state.Connections))
}

func cloneManagerState(state managerState) managerState {
	connections := make([]ConnectionConfig, len(state.Connections))
	for i, cfg := range state.Connections {
		connections[i] = cloneConnectionConfig(cfg)
	}
	return managerState{Connections: connections}
}

func cloneConnectionConfig(cfg ConnectionConfig) ConnectionConfig {
	cfg.Args = slices.Clone(cfg.Args)
	cfg.Env = slices.Clone(cfg.Env)
	return cfg
}

func validateCandidateConnection(cfg ConnectionConfig, connections []ConnectionConfig, replacing int) error {
	if err := validateServerBoundConnection(cfg); err != nil {
		return err
	}
	if cfg.Transport == "sse" {
		if cfg.URL == "" {
			return fmt.Errorf("url is required for sse transport")
		}
	} else if cfg.Command != "" {
		if err := validateCommand(cfg.Command); err != nil {
			return err
		}
	}
	for i, existing := range connections {
		if i == replacing {
			continue
		}
		if existing.ID == cfg.ID {
			return fmt.Errorf("connection %q already exists", cfg.ID)
		}
		if cfg.ContainerID != "" && cfg.NodeletID != "" &&
			existing.ContainerID == cfg.ContainerID && existing.NodeletID == cfg.NodeletID {
			return fmt.Errorf("container %q on nodelet %q already has connection %q", cfg.ContainerID, cfg.NodeletID, existing.ID)
		}
	}
	return nil
}

func mcpConnectionsToRuntime(connections []ConnectionConfig) []runtimestore.MCPConnectionRecord {
	records := make([]runtimestore.MCPConnectionRecord, len(connections))
	for i, c := range connections {
		c = normalizeConnectionConfig(c)
		records[i] = runtimestore.MCPConnectionRecord{
			ID:          c.ID,
			Name:        c.Name,
			Type:        c.Type,
			Transport:   c.Transport,
			Command:     c.Command,
			Args:        append([]string{}, c.Args...),
			Env:         append([]string{}, c.Env...),
			URL:         c.URL,
			Enabled:     c.Enabled,
			ContainerID: c.ContainerID,
			NodeletID:   c.NodeletID,
		}
	}
	return records
}

func mcpConnectionFromRuntime(r runtimestore.MCPConnectionRecord) ConnectionConfig {
	return ConnectionConfig{
		ID:          r.ID,
		Name:        r.Name,
		Type:        r.Type,
		Transport:   r.Transport,
		Command:     r.Command,
		Args:        append([]string{}, r.Args...),
		Env:         append([]string{}, r.Env...),
		URL:         r.URL,
		Enabled:     r.Enabled,
		ContainerID: r.ContainerID,
		NodeletID:   r.NodeletID,
	}
}
