package nodelet

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"oops/internal/logutil"
	"oops/internal/store"

	"go.uber.org/zap"
)

// DefaultConfigPath is the default path for the nodelet configuration file.
const DefaultConfigPath = "config/nodelets.json"

// FallbackConfigPath is used when the primary config cannot be loaded.
const FallbackConfigPath = "/dev/null"

// NodeletConfig 保存一台 oops-nodelet 的访问信息。
// Token 在 JSON API 响应中永远不暴露（json:"-"），
// 但通过 HasToken 告知前端是否已设置 token。
type NodeletConfig struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Address  string `json:"address"`
	Token    string `json:"-"`
	HasToken bool   `json:"hasToken"`
}

// persistedNodelet 是 nodelets.json 的磁盘格式 —— 包含 token。
type persistedNodelet struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address"`
	Token   string `json:"token"`
}

func (p persistedNodelet) toConfig() NodeletConfig {
	return NodeletConfig{ID: p.ID, Name: p.Name, Address: p.Address, Token: p.Token, HasToken: p.Token != ""}
}

func toPersisted(cfg NodeletConfig) persistedNodelet {
	return persistedNodelet{ID: cfg.ID, Name: cfg.Name, Address: cfg.Address, Token: cfg.Token}
}

// nodeletConfigFile 是 manager 持久化文件的顶层结构。
type nodeletConfigFile struct {
	Nodelets []persistedNodelet `json:"nodelets"`
}

// NodeletManager 管理 nodelet 配置的 CRUD，持久化到 JSON 文件。
type NodeletManager struct {
	mu         sync.Mutex
	configPath string
	config     nodeletConfigFile
}

// NewNodeletManager 加载 JSON 文件，不存在时初始化为空列表。
// configPath 为空时创建纯内存 manager（测试用途）。
func NewNodeletManager(configPath string) (*NodeletManager, error) {
	m := &NodeletManager{configPath: configPath}
	if configPath == "" {
		return m, nil
	}
	if err := m.load(); err != nil {
		return nil, fmt.Errorf("load nodelets: %w", err)
	}
	return m, nil
}

// List 返回所有 nodelet 配置的副本。
func (m *NodeletManager) List() []NodeletConfig {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]NodeletConfig, len(m.config.Nodelets))
	for i, n := range m.config.Nodelets {
		out[i] = n.toConfig()
	}
	return out
}

// Find 按 ID 查找 nodelet。
func (m *NodeletManager) Find(id string) (NodeletConfig, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, n := range m.config.Nodelets {
		if n.ID == id {
			return n.toConfig(), true
		}
	}
	logutil.Warn("nodelet manager: find missed", zap.String("id", id))
	return NodeletConfig{}, false
}

// Add 新增一条 nodelet 配置并持久化。
func (m *NodeletManager) Add(cfg NodeletConfig) error {
	if cfg.ID == "" {
		return fmt.Errorf("id is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, existing := range m.config.Nodelets {
		if existing.ID == cfg.ID {
			logutil.Warn("nodelet manager: add skipped, already exists", zap.String("id", cfg.ID))
			return fmt.Errorf("nodelet %q already exists", cfg.ID)
		}
	}

	m.config.Nodelets = append(m.config.Nodelets, toPersisted(cfg))
	if err := m.saveLocked(); err != nil {
		return err
	}
	logutil.Info("nodelet manager: add",
		zap.String("id", cfg.ID),
		zap.String("name", cfg.Name),
		zap.String("address", cfg.Address),
		zap.Bool("hasToken", cfg.HasToken),
	)
	return nil
}

// Update 修改已有 nodelet 配置并持久化。
func (m *NodeletManager) Update(cfg NodeletConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	idx := -1
	for i, existing := range m.config.Nodelets {
		if existing.ID == cfg.ID {
			idx = i
			break
		}
	}
	if idx < 0 {
		logutil.Warn("nodelet manager: update skipped, not found", zap.String("id", cfg.ID))
		return fmt.Errorf("nodelet %q not found", cfg.ID)
	}

	m.config.Nodelets[idx] = toPersisted(cfg)
	if err := m.saveLocked(); err != nil {
		return err
	}
	logutil.Info("nodelet manager: update",
		zap.String("id", cfg.ID),
		zap.String("name", cfg.Name),
		zap.String("address", cfg.Address),
		zap.Bool("hasToken", cfg.HasToken),
	)
	return nil
}

// Remove 删除一条 nodelet 配置并持久化。
func (m *NodeletManager) Remove(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	idx := -1
	for i, existing := range m.config.Nodelets {
		if existing.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		logutil.Warn("nodelet manager: remove skipped, not found", zap.String("id", id))
		return fmt.Errorf("nodelet %q not found", id)
	}

	m.config.Nodelets = append(m.config.Nodelets[:idx], m.config.Nodelets[idx+1:]...)
	if err := m.saveLocked(); err != nil {
		return err
	}
	logutil.Info("nodelet manager: remove", zap.String("id", id))
	return nil
}

// Test 尝试连接 nodelet 的 /health 端点验证配置有效。
// 最多重试 3 次，每次间隔递增（1s / 2s / 3s）。
func (m *NodeletManager) Test(cfg NodeletConfig) error {
	const maxRetries = 3
	client := &http.Client{Timeout: 6 * time.Second}

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}

		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, cfg.Address+"/health", nil)
		if err != nil {
			return fmt.Errorf("bad address: %w", err)
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			return nil
		}
		lastErr = fmt.Errorf("health returned %d", resp.StatusCode)
	}

	return fmt.Errorf("connect (×%d): %w", maxRetries, lastErr)
}

// --- internal ---

func (m *NodeletManager) load() error {
	if err := store.LoadJSON(m.configPath, &m.config); err != nil {
		return err
	}
	if m.config.Nodelets == nil {
		m.config.Nodelets = []persistedNodelet{}
	}
	return nil
}

func (m *NodeletManager) saveLocked() error {
	if m.configPath == "" {
		return nil
	}
	return store.SaveJSON(m.configPath, m.config)
}
