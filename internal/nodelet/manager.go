package nodelet

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"oops/internal/logutil"
	runtimestore "oops/internal/store/runtime"

	"go.uber.org/zap"
)

// NodeletConfig 保存一台 oops-nodelet 的访问信息。
// ID 是系统自动生成的主键（用户不可见），Name 是用户指定的唯一展示名称。
// Token 在 JSON API 响应中永远不暴露（json:"-"），
// 但通过 HasToken 告知前端是否已设置 token。
type NodeletConfig struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Address  string `json:"address"`
	Token    string `json:"-"`
	HasToken bool   `json:"hasToken"`
}

// persistedNodelet 是运行态持久化形态，包含 token。
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

type nodeletState struct {
	Nodelets    []persistedNodelet `json:"nodelets"`
	NextCounter int                `json:"nextCounter"`
}

// NodeletManager 管理 nodelet 配置的 CRUD，运行态持久化到 SQLite。
type NodeletManager struct {
	mu      sync.Mutex
	runtime *runtimestore.Store
	config  nodeletState
}

// NewNodeletManagerWithRuntime loads nodelets from SQLite.
func NewNodeletManagerWithRuntime(runtime *runtimestore.Store) (*NodeletManager, error) {
	if runtime == nil {
		return nil, fmt.Errorf("runtime store is required")
	}
	m := &NodeletManager{runtime: runtime}
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

// Add 新增一条 nodelet 配置并持久化。ID 自动生成（写入 cfg.ID），Name 必须唯一。
func (m *NodeletManager) Add(cfg *NodeletConfig) error {
	if cfg.Name == "" {
		return fmt.Errorf("name is required")
	}
	if cfg.Token == "" {
		return fmt.Errorf("token is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, existing := range m.config.Nodelets {
		if existing.Name == cfg.Name {
			logutil.Warn("nodelet manager: add skipped, name already exists", zap.String("name", cfg.Name))
			return fmt.Errorf("nodelet %q already exists", cfg.Name)
		}
	}

	if cfg.ID == "" {
		cfg.ID = m.nextIDLocked()
	}
	m.config.Nodelets = append(m.config.Nodelets, toPersisted(*cfg))
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

// Update 修改已有 nodelet 配置并持久化。按 ID 查找，Name 不可与已有冲突。
func (m *NodeletManager) Update(cfg NodeletConfig) error {
	if cfg.Token == "" {
		return fmt.Errorf("token is required")
	}
	if cfg.ID == "" {
		return fmt.Errorf("id is required")
	}

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

	// 检查 name 是否与其他已有条目冲突（自身除外）
	for _, existing := range m.config.Nodelets {
		if existing.ID != cfg.ID && existing.Name == cfg.Name {
			return fmt.Errorf("nodelet name %q already exists", cfg.Name)
		}
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

// nextIDLocked 生成下一个自增 ID。调用方需持有锁。
func (m *NodeletManager) nextIDLocked() string {
	m.config.NextCounter++
	return strconv.Itoa(m.config.NextCounter)
}

// Test 尝试连接 nodelet 的 /host 端点验证 token 有效。
// 最多重试 3 次，每次间隔递增（1s / 2s / 3s）。
func (m *NodeletManager) Test(cfg NodeletConfig) error {
	const maxRetries = 3
	client := &http.Client{Timeout: 6 * time.Second}

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}

		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, cfg.Address+"/host", nil)
		if err != nil {
			return fmt.Errorf("bad address: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+cfg.Token)

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			return nil
		}
		if resp.StatusCode == http.StatusUnauthorized {
			return fmt.Errorf("unauthorized: token mismatch")
		}
		lastErr = fmt.Errorf("host returned %d", resp.StatusCode)
	}

	return fmt.Errorf("connect (×%d): %w", maxRetries, lastErr)
}

// --- internal ---

func (m *NodeletManager) load() error {
	ctx := context.Background()
	records, err := m.runtime.ListNodelets(ctx)
	if err != nil {
		return err
	}
	m.config.Nodelets = nodeletsFromRuntime(records)
	if m.config.Nodelets == nil {
		m.config.Nodelets = []persistedNodelet{}
	}
	m.config.NextCounter = maxNumericNodeletID(m.config.Nodelets)
	return nil
}

func (m *NodeletManager) saveLocked() error {
	return m.runtime.ReplaceNodelets(context.Background(), nodeletsToRuntime(m.config.Nodelets))
}

func nodeletsToRuntime(nodelets []persistedNodelet) []runtimestore.NodeletRecord {
	records := make([]runtimestore.NodeletRecord, len(nodelets))
	for i, n := range nodelets {
		records[i] = runtimestore.NodeletRecord{
			ID:      n.ID,
			Name:    n.Name,
			Address: n.Address,
			Token:   n.Token,
		}
	}
	return records
}

func nodeletsFromRuntime(records []runtimestore.NodeletRecord) []persistedNodelet {
	nodelets := make([]persistedNodelet, len(records))
	for i, r := range records {
		nodelets[i] = persistedNodelet{
			ID:      r.ID,
			Name:    r.Name,
			Address: r.Address,
			Token:   r.Token,
		}
	}
	return nodelets
}

func maxNumericNodeletID(nodelets []persistedNodelet) int {
	maxID := 0
	for _, n := range nodelets {
		id, err := strconv.Atoi(n.ID)
		if err == nil && id > maxID {
			maxID = id
		}
	}
	return maxID
}
