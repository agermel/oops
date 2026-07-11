package nodelet

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"oops/internal/logutil"

	"go.uber.org/zap"
)

// ProbeStatus 表示 Nodelet 的连通性状态。
type ProbeStatus string

const (
	StatusUnknown   ProbeStatus = "unknown"
	StatusProbing   ProbeStatus = "probing"
	StatusHealthy   ProbeStatus = "healthy"
	StatusUnhealthy ProbeStatus = "unhealthy"
	StatusDead      ProbeStatus = "dead"
)

// ProbeResult 是单个 Nodelet 的探测结果快照。
type ProbeResult struct {
	NodeletID        string      `json:"nodeletId"`
	Status           ProbeStatus `json:"status"`
	ConsecutiveFails int         `json:"consecutiveFails"`
	LastProbeAt      time.Time   `json:"lastProbeAt"`
	LastError        string      `json:"lastError,omitempty"`
	LatencyMs        int64       `json:"latencyMs"`
}

// NodeletProber 在后台周期性探测所有 Nodelet 的 /host 端点（带 Bearer token），
// 维护一份可缓存的连通性状态，供 API 和前端统一读取。
type NodeletProber struct {
	mu      sync.Mutex
	cond    *sync.Cond
	manager *NodeletManager

	results map[string]*ProbeResult // nodeletID → 最新状态
	probing map[string]struct{}     // 正在探测中的 nodeletID 集合

	healthInterval time.Duration // 健康节点探测间隔（默认 1min）
	deadInterval   time.Duration // 死亡节点探测间隔（默认 10min）
	probeTimeout   time.Duration // 单次探测 HTTP 超时（默认 5s）
	deathThreshold int           // 连续失败多少次判死（默认 3）

	httpClient *http.Client
	lifecycle  context.Context
	cancel     context.CancelFunc
	doneCh     chan struct{}
	workers    sync.WaitGroup
	started    bool
	stopped    bool
}

// NewNodeletProber 创建 Prober 并初始化内部状态。
func NewNodeletProber(manager *NodeletManager) *NodeletProber {
	lifecycle, cancel := context.WithCancel(context.Background())
	p := &NodeletProber{
		manager:        manager,
		results:        make(map[string]*ProbeResult),
		probing:        make(map[string]struct{}),
		healthInterval: 1 * time.Minute,
		deadInterval:   10 * time.Minute,
		probeTimeout:   5 * time.Second,
		deathThreshold: 3,
		httpClient:     &http.Client{Timeout: 5 * time.Second},
		lifecycle:      lifecycle,
		cancel:         cancel,
		doneCh:         make(chan struct{}),
	}
	p.cond = sync.NewCond(&p.mu)
	// 从 manager 预填所有已知 nodelet 条目
	for _, cfg := range manager.List() {
		p.results[cfg.ID] = &ProbeResult{
			NodeletID: cfg.ID,
			Status:    StatusUnknown,
		}
	}
	return p
}

// Start 启动后台探测循环。首次启动会立即探测全部节点。
func (p *NodeletProber) Start() {
	p.mu.Lock()
	if p.started || p.stopped {
		p.mu.Unlock()
		return
	}
	p.started = true
	p.workers.Add(2)
	p.mu.Unlock()

	logutil.Info("nodelet prober: starting",
		zap.Int("nodeletCount", len(p.results)),
		zap.Duration("healthInterval", p.healthInterval),
		zap.Duration("deadInterval", p.deadInterval),
		zap.Int("deathThreshold", p.deathThreshold),
	)

	go func() {
		defer p.workers.Done()
		p.loop()
	}()
	go func() {
		defer p.workers.Done()
		p.initialProbe()
	}()
	go func() {
		p.workers.Wait()
		close(p.doneCh)
	}()
}

// Stop 停止后台探测循环。
func (p *NodeletProber) Stop() {
	p.mu.Lock()
	if !p.started {
		if !p.stopped {
			p.stopped = true
			if p.cancel != nil {
				p.cancel()
			}
		}
		p.mu.Unlock()
		return
	}
	if !p.stopped {
		p.stopped = true
		cancel := p.cancel
		p.mu.Unlock()
		logutil.Info("nodelet prober: stopping")
		if cancel != nil {
			cancel()
		}
	} else {
		p.mu.Unlock()
	}
	<-p.doneCh
	logutil.Info("nodelet prober: stopped")
}

// Status 返回所有 Nodelet 的当前缓存状态。
func (p *NodeletProber) Status() []ProbeResult {
	p.mu.Lock()
	defer p.mu.Unlock()

	out := make([]ProbeResult, 0, len(p.results))
	for _, r := range p.results {
		out = append(out, *r)
	}
	return out
}

// StatusByID 返回单个 Nodelet 的缓存状态，不存在时返回 nil。
func (p *NodeletProber) StatusByID(id string) *ProbeResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	r, ok := p.results[id]
	if !ok {
		return nil
	}
	cp := *r
	return &cp
}

// ProbeNow 立即探测指定 Nodelet（如果已在探测中则等待当前结果）。
func (p *NodeletProber) ProbeNow(nodeletID string) ProbeResult {
	logutil.Debug("nodelet prober: probe requested", zap.String("nodeletID", nodeletID))

	cfg, ok := p.manager.Find(nodeletID)
	if !ok {
		logutil.Warn("nodelet prober: probe skipped, nodelet not found", zap.String("nodeletID", nodeletID))
		return ProbeResult{NodeletID: nodeletID, Status: StatusDead, LastError: "nodelet not found"}
	}

	p.mu.Lock()
	_, alreadyProbing := p.probing[nodeletID]
	if alreadyProbing {
		for {
			_, stillProbing := p.probing[nodeletID]
			if !stillProbing {
				p.mu.Unlock()
				return *p.StatusByID(nodeletID)
			}
			p.cond.Wait()
		}
	}
	p.probing[nodeletID] = struct{}{}
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		delete(p.probing, nodeletID)
		p.cond.Broadcast()
		p.mu.Unlock()
	}()

	return p.probeOne(cfg)
}

// ProbeAll 强制立即探测全部 Nodelet。
func (p *NodeletProber) ProbeAll() {
	cfgs := p.manager.List()
	var wg sync.WaitGroup
	for _, cfg := range cfgs {
		p.mu.Lock()
		_, already := p.probing[cfg.ID]
		if !already {
			p.probing[cfg.ID] = struct{}{}
		}
		p.mu.Unlock()
		if already {
			continue
		}

		wg.Add(1)
		go func(c NodeletConfig) {
			defer wg.Done()
			defer func() {
				p.mu.Lock()
				delete(p.probing, c.ID)
				p.cond.Broadcast()
				p.mu.Unlock()
			}()
			p.probeOne(c)
		}(cfg)
	}
	wg.Wait()
}

// OnConfigChange 在 NodeletManager 配置变更（增/删/改）后调用，
// 同步 Prober 内部状态。
func (p *NodeletProber) OnConfigChange() {
	p.mu.Lock()
	defer p.mu.Unlock()

	current := p.manager.List()
	currentIDs := make(map[string]bool, len(current))

	// 新增或更新：补上 unknown 条目（如果还没有）
	for _, cfg := range current {
		currentIDs[cfg.ID] = true
		if _, exists := p.results[cfg.ID]; !exists {
			logutil.Info("nodelet prober: config add", zap.String("nodeletID", cfg.ID))
			p.results[cfg.ID] = &ProbeResult{
				NodeletID: cfg.ID,
				Status:    StatusUnknown,
			}
		}
	}

	// 删除：移除已不存在的条目
	for id := range p.results {
		if !currentIDs[id] {
			logutil.Info("nodelet prober: config remove", zap.String("nodeletID", id))
			delete(p.results, id)
		}
	}
}

// --- internal ---

// loop 是后台探测主循环。
func (p *NodeletProber) loop() {
	ticker := time.NewTicker(5 * time.Second) // 每 5s 检查一次哪些节点该探测了
	defer ticker.Stop()

	for {
		select {
		case <-p.lifecycle.Done():
			return
		case <-ticker.C:
			p.tick()
		}
	}
}

func (p *NodeletProber) initialProbe() {
	p.mu.Lock()
	ids := make([]string, 0, len(p.results))
	for id := range p.results {
		ids = append(ids, id)
	}
	p.mu.Unlock()

	for _, id := range ids {
		select {
		case <-p.lifecycle.Done():
			return
		default:
		}
		cfg, ok := p.manager.Find(id)
		if !ok {
			continue
		}
		p.probeOne(cfg)
	}
	logutil.Info("nodelet prober: initial probe round complete")
}

// tick 检查每个节点是否到了该探测的时间。
func (p *NodeletProber) tick() {
	select {
	case <-p.lifecycle.Done():
		return
	default:
	}

	cfgs := p.manager.List()
	now := time.Now()

	for _, cfg := range cfgs {
		p.mu.Lock()
		r, exists := p.results[cfg.ID]
		if !exists {
			p.mu.Unlock()
			continue
		}

		// 跳过正在探测中的节点
		if _, probing := p.probing[cfg.ID]; probing {
			p.mu.Unlock()
			continue
		}

		// 决定探测间隔
		interval := p.healthInterval
		if r.Status == StatusDead {
			interval = p.deadInterval
		}

		// 检查是否到了探测时间
		if now.Sub(r.LastProbeAt) < interval {
			p.mu.Unlock()
			continue
		}

		// 标记为探测中
		p.probing[cfg.ID] = struct{}{}
		p.mu.Unlock()

		// 异步探测（不阻塞 tick 循环）
		p.workers.Add(1)
		go func(c NodeletConfig) {
			defer p.workers.Done()
			defer func() {
				p.mu.Lock()
				delete(p.probing, c.ID)
				p.cond.Broadcast()
				p.mu.Unlock()
			}()
			p.probeOne(c)
		}(cfg)
	}
}

// probeOne 对单个 Nodelet 执行一次探测并更新状态。
// 调用方负责管理 probing 集合的加入/移除。
// token 为必填项，统一走 GET /host（带 Bearer token）。
// 401 → token 不匹配，其他非 200 → 不可达。
func (p *NodeletProber) probeOne(cfg NodeletConfig) ProbeResult {
	start := time.Now()

	ctx, cancel := context.WithTimeout(p.lifecycle, p.probeTimeout)
	defer cancel()

	endpoint := cfg.Address + "/host"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return p.applyResult(cfg.ID, false, fmt.Sprintf("bad address: %v", err), 0)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)

	resp, err := p.httpClient.Do(req)
	latency := time.Since(start).Milliseconds()

	if err != nil {
		return p.applyResult(cfg.ID, false, err.Error(), latency)
	}
	resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return p.applyResult(cfg.ID, true, "", latency)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return p.applyResult(cfg.ID, false, "unauthorized: token mismatch", latency)
	}

	return p.applyResult(cfg.ID, false, fmt.Sprintf("returned %d", resp.StatusCode), latency)
}

// applyResult 更新 Prober 内部状态并返回 ProbeResult。
func (p *NodeletProber) applyResult(nodeletID string, success bool, errMsg string, latency int64) ProbeResult {
	p.mu.Lock()
	defer p.mu.Unlock()

	r, exists := p.results[nodeletID]
	if !exists {
		r = &ProbeResult{NodeletID: nodeletID, Status: StatusUnknown}
		p.results[nodeletID] = r
	}

	prevStatus := r.Status
	r.LastProbeAt = time.Now()
	r.LatencyMs = latency

	if success {
		r.ConsecutiveFails = 0
		r.LastError = ""
		r.Status = StatusHealthy
	} else {
		r.ConsecutiveFails++
		r.LastError = errMsg
		if r.ConsecutiveFails >= p.deathThreshold {
			r.Status = StatusDead
		} else {
			r.Status = StatusUnhealthy
		}
	}

	// 仅在状态发生迁移时写日志
	if r.Status != prevStatus {
		switch r.Status {
		case StatusHealthy:
			if prevStatus == StatusDead {
				logutil.Info("nodelet prober: node recovered from dead",
					zap.String("nodeletID", nodeletID),
					zap.Int64("latencyMs", latency),
				)
			} else {
				logutil.Info("nodelet prober: node healthy",
					zap.String("nodeletID", nodeletID),
					zap.Int64("latencyMs", latency),
				)
			}
		case StatusUnhealthy:
			logutil.Warn("nodelet prober: node degraded",
				zap.String("nodeletID", nodeletID),
				zap.Int("consecutiveFails", r.ConsecutiveFails),
				zap.String("error", errMsg),
			)
		case StatusDead:
			logutil.Error("nodelet prober: node dead",
				zap.String("nodeletID", nodeletID),
				zap.Int("consecutiveFails", r.ConsecutiveFails),
				zap.String("error", errMsg),
			)
		}
	}

	cp := *r
	return cp
}
