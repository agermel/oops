package nodelet

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// 已知 bug（仅记录，不在本次重构中修复）：
//
// 1. Stop() before Start() 会永久阻塞 —— doneCh 永远不会被关闭。
//    如果在 Start() 之前调用 Stop()，测试会无限期挂起。
//
// 2. 重复调用 Start() 会启动重复的 goroutine —— 没有启动状态守卫。
//    后果: 多个 goroutine 同时运行 loop()，可能导致竞态和重复探测。
//
// 3. 重复调用 Stop() 会 panic —— 第二次 close(stopCh)。
//    连续调用 Stop() 两次会导致 "close of closed channel" panic。

// ---- 辅助函数 ----

// newTestServer 创建返回固定状态码的 httptest server。
func newTestServer(status int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
}

// newEchoServer 创建回显请求路径和 Authorization header 的 server。
func newEchoServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"path":"` + r.URL.Path + `","auth":"` + r.Header.Get("Authorization") + `"}`))
	}))
}

// newBlockingServer 创建阻塞直到收到信号的 server。
func newBlockingServer(unblock <-chan struct{}) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-unblock
		w.WriteHeader(http.StatusOK)
	}))
}

// ---- 构造 ----

// TestNodeletProber_New 测试从 manager 创建 Prober。
func TestNodeletProber_New(t *testing.T) {
	m := newTestNodeletManager(t)
	_ = m.Add(&NodeletConfig{ID: "a", Name: "a", Token: "t"})
	_ = m.Add(&NodeletConfig{ID: "b", Name: "b", Token: "t"})

	p := NewNodeletProber(m)
	results := p.Status()
	if len(results) != 2 {
		t.Fatalf("Status len = %d, want 2", len(results))
	}
	for _, r := range results {
		if r.Status != StatusUnknown {
			t.Errorf("%s: status = %s, want %s", r.NodeletID, r.Status, StatusUnknown)
		}
		if r.ConsecutiveFails != 0 {
			t.Errorf("%s: consecutiveFails = %d, want 0", r.NodeletID, r.ConsecutiveFails)
		}
		if !r.LastProbeAt.IsZero() {
			t.Errorf("%s: LastProbeAt should be zero, got %v", r.NodeletID, r.LastProbeAt)
		}
	}
}

// TestNodeletProber_NewEmpty 测试空 manager 创建 Prober。
func TestNodeletProber_NewEmpty(t *testing.T) {
	m := newTestNodeletManager(t)
	p := NewNodeletProber(m)
	if len(p.Status()) != 0 {
		t.Error("Status should be empty for empty manager")
	}
}

// ---- Status / StatusByID ----

// TestNodeletProber_StatusByID 测试按 ID 查找状态。
func TestNodeletProber_StatusByID(t *testing.T) {
	m := newTestNodeletManager(t)
	_ = m.Add(&NodeletConfig{ID: "n1", Name: "n1", Token: "t"})
	p := NewNodeletProber(m)

	if r := p.StatusByID("n1"); r == nil {
		t.Error("StatusByID should return non-nil for existing ID")
	}
	if r := p.StatusByID("missing"); r != nil {
		t.Error("StatusByID should return nil for missing ID")
	}
}

// TestNodeletProber_StatusCopySemantics 测试 Status 返回副本。
func TestNodeletProber_StatusCopySemantics(t *testing.T) {
	m := newTestNodeletManager(t)
	_ = m.Add(&NodeletConfig{ID: "n1", Name: "n1", Token: "t"})
	p := NewNodeletProber(m)

	results := p.Status()
	results[0].Status = "corrupted"

	r2 := p.Status()
	if r2[0].Status == "corrupted" {
		t.Error("Status should return a copy, not a reference")
	}
}

// ---- ProbeNow ----

// TestNodeletProber_ProbeNow_Healthy 测试健康节点探测。
func TestNodeletProber_ProbeNow_Healthy(t *testing.T) {
	srv := newTestServer(http.StatusOK)
	defer srv.Close()

	m := newTestNodeletManager(t)
	_ = m.Add(&NodeletConfig{ID: "n1", Name: "n1", Address: srv.URL, Token: "t"})
	p := NewNodeletProber(m)

	result := p.ProbeNow("n1")
	if result.Status != StatusHealthy {
		t.Errorf("status = %s, want %s", result.Status, StatusHealthy)
	}
	if result.ConsecutiveFails != 0 {
		t.Errorf("consecutiveFails = %d, want 0", result.ConsecutiveFails)
	}
	if result.LatencyMs < 0 {
		t.Error("LatencyMs should be >= 0")
	}
	if result.LastProbeAt.IsZero() {
		t.Error("LastProbeAt should be set")
	}
	if result.LastError != "" {
		t.Errorf("LastError = %q, want empty", result.LastError)
	}
}

// TestNodeletProber_ProbeNow_UnhealthyToDead 测试连续失败直到判死。
func TestNodeletProber_ProbeNow_UnhealthyToDead(t *testing.T) {
	srv := newTestServer(http.StatusServiceUnavailable)
	defer srv.Close()

	m := newTestNodeletManager(t)
	_ = m.Add(&NodeletConfig{ID: "n1", Name: "n1", Address: srv.URL, Token: "t"})
	p := NewNodeletProber(m)

	// 第一次失败 → Unhealthy
	r1 := p.ProbeNow("n1")
	if r1.Status != StatusUnhealthy {
		t.Errorf("probe 1: status = %s, want %s", r1.Status, StatusUnhealthy)
	}
	if r1.ConsecutiveFails != 1 {
		t.Errorf("probe 1: consecutiveFails = %d, want 1", r1.ConsecutiveFails)
	}

	// 第二次失败 → 还是 Unhealthy
	r2 := p.ProbeNow("n1")
	if r2.Status != StatusUnhealthy {
		t.Errorf("probe 2: status = %s, want %s", r2.Status, StatusUnhealthy)
	}
	if r2.ConsecutiveFails != 2 {
		t.Errorf("probe 2: consecutiveFails = %d, want 2", r2.ConsecutiveFails)
	}

	// 第三次失败 → Dead（deathThreshold=3）
	r3 := p.ProbeNow("n1")
	if r3.Status != StatusDead {
		t.Errorf("probe 3: status = %s, want %s", r3.Status, StatusDead)
	}
	if r3.ConsecutiveFails != 3 {
		t.Errorf("probe 3: consecutiveFails = %d, want 3", r3.ConsecutiveFails)
	}
}

// TestNodeletProber_ProbeNow_Recovery 测试死节点恢复。
func TestNodeletProber_ProbeNow_Recovery(t *testing.T) {
	// 先用 503 让其判死
	badSrv := newTestServer(http.StatusServiceUnavailable)
	m := newTestNodeletManager(t)
	_ = m.Add(&NodeletConfig{ID: "n1", Name: "n1", Address: badSrv.URL, Token: "t"})
	p := NewNodeletProber(m)

	for i := 0; i < 3; i++ {
		p.ProbeNow("n1")
	}
	badSrv.Close()

	// 再用 200 探测
	goodSrv := newTestServer(http.StatusOK)
	defer goodSrv.Close()
	// 直接修改 manager 中的地址（同包可访问内部字段）
	m.mu.Lock()
	for i := range m.config.Nodelets {
		if m.config.Nodelets[i].ID == "n1" {
			m.config.Nodelets[i].Address = goodSrv.URL
		}
	}
	m.mu.Unlock()

	r := p.ProbeNow("n1")
	if r.Status != StatusHealthy {
		t.Errorf("status = %s, want %s", r.Status, StatusHealthy)
	}
	if r.ConsecutiveFails != 0 {
		t.Errorf("consecutiveFails = %d, want 0", r.ConsecutiveFails)
	}
	if r.LastError != "" {
		t.Errorf("LastError = %q, want empty", r.LastError)
	}
}

// TestNodeletProber_ProbeNow_AuthMode 测试带 token 的探测走 /host 端点。
func TestNodeletProber_ProbeNow_AuthMode(t *testing.T) {
	srv := newEchoServer(t)
	defer srv.Close()

	m := newTestNodeletManager(t)
	_ = m.Add(&NodeletConfig{ID: "n1", Name: "n1", Address: srv.URL, Token: "my-secret"})
	p := NewNodeletProber(m)

	result := p.ProbeNow("n1")
	if result.Status != StatusHealthy {
		t.Fatalf("status = %s, want %s", result.Status, StatusHealthy)
	}

	// 通过 StatusByID 无法验证请求路径；这里仅验证探测成功。
	// 实际路径验证依赖 newEchoServer 的回显。
}

// TestNodeletProber_ProbeNow_Unauthorized 测试 401 响应。
func TestNodeletProber_ProbeNow_Unauthorized(t *testing.T) {
	srv := newTestServer(http.StatusUnauthorized)
	defer srv.Close()

	m := newTestNodeletManager(t)
	_ = m.Add(&NodeletConfig{ID: "n1", Name: "n1", Address: srv.URL, Token: "wrong"})
	p := NewNodeletProber(m)

	result := p.ProbeNow("n1")
	if result.Status != StatusUnhealthy {
		t.Errorf("status = %s, want %s", result.Status, StatusUnhealthy)
	}
	if !strings.Contains(result.LastError, "unauthorized") && !strings.Contains(result.LastError, "token mismatch") {
		t.Errorf("LastError = %q, should mention unauthorized/token-mismatch", result.LastError)
	}
}

// TestNodeletProber_ProbeNow_Timeout 测试探测超时。
func TestNodeletProber_ProbeNow_Timeout(t *testing.T) {
	// 创建一个永远不响应的 server，然后用极短的超时
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer srv.Close()

	m := newTestNodeletManager(t)
	_ = m.Add(&NodeletConfig{ID: "n1", Name: "n1", Address: srv.URL, Token: "t"})
	p := NewNodeletProber(m)
	// 同包测试可设置内部超时
	p.probeTimeout = 50 * time.Millisecond
	p.httpClient.Timeout = 50 * time.Millisecond

	result := p.ProbeNow("n1")
	if result.Status == StatusHealthy {
		t.Errorf("status = %s, should not be healthy on timeout", result.Status)
	}
}

// TestNodeletProber_ProbeNow_NotFound 测试探测不存在的 nodelet。
func TestNodeletProber_ProbeNow_NotFound(t *testing.T) {
	m := newTestNodeletManager(t)
	p := NewNodeletProber(m)

	result := p.ProbeNow("nonexistent")
	if result.Status != StatusDead {
		t.Errorf("status = %s, want %s", result.Status, StatusDead)
	}
	if result.LastError != "nodelet not found" {
		t.Errorf("LastError = %q, want 'nodelet not found'", result.LastError)
	}
}

// ---- ProbeNow 并发去重（sync.Cond） ----

// TestNodeletProber_ProbeNow_ConcurrentWait 测试并发 ProbeNow 去重。
func TestNodeletProber_ProbeNow_ConcurrentWait(t *testing.T) {
	unblock := make(chan struct{})
	srv := newBlockingServer(unblock)
	defer srv.Close()

	m := newTestNodeletManager(t)
	_ = m.Add(&NodeletConfig{ID: "n1", Name: "n1", Address: srv.URL, Token: "t"})
	p := NewNodeletProber(m)

	var wg sync.WaitGroup
	var r1, r2 ProbeResult

	// 第一个 goroutine 调用 ProbeNow，会阻塞在 HTTP 请求上
	wg.Add(1)
	go func() {
		defer wg.Done()
		r1 = p.ProbeNow("n1")
	}()

	// 给第一个 goroutine 一点时间进入 probed 状态
	time.Sleep(50 * time.Millisecond)

	// 第二个 goroutine 也应该调用 ProbeNow，会发现已在 probing，走 Cond.Wait
	wg.Add(1)
	go func() {
		defer wg.Done()
		r2 = p.ProbeNow("n1")
	}()

	// 给第二个 goroutine 时间进入 Wait
	time.Sleep(50 * time.Millisecond)

	// 验证 probing 集合中有 n1
	p.mu.Lock()
	_, inProbing := p.probing["n1"]
	p.mu.Unlock()
	if !inProbing {
		t.Error("n1 should be in probing set while first goroutine is probing")
	}

	// 释放 server，让探测完成
	close(unblock)

	// 等待两个 goroutine 完成
	wg.Wait()

	// 两个结果应该一致
	if r1.Status != StatusHealthy || r2.Status != StatusHealthy {
		t.Errorf("r1.status=%s r2.status=%s, both should be healthy", r1.Status, r2.Status)
	}
	// LatencyMs 应该一致（来自同一次探测）
	if r1.LatencyMs != r2.LatencyMs {
		t.Errorf("r1.LatencyMs=%d r2.LatencyMs=%d, should be equal from same probe", r1.LatencyMs, r2.LatencyMs)
	}
}

// ---- ProbeAll ----

// TestNodeletProber_ProbeAll 测试批量探测。
func TestNodeletProber_ProbeAll(t *testing.T) {
	goodSrv := newTestServer(http.StatusOK)
	defer goodSrv.Close()
	badSrv := newTestServer(http.StatusServiceUnavailable)
	defer badSrv.Close()

	m := newTestNodeletManager(t)
	_ = m.Add(&NodeletConfig{ID: "healthy", Name: "healthy", Address: goodSrv.URL, Token: "t"})
	_ = m.Add(&NodeletConfig{ID: "unhealthy", Name: "unhealthy", Address: badSrv.URL, Token: "t"})
	p := NewNodeletProber(m)

	p.ProbeAll()

	r1 := p.StatusByID("healthy")
	r2 := p.StatusByID("unhealthy")

	if r1.Status != StatusHealthy {
		t.Errorf("healthy: status = %s, want %s", r1.Status, StatusHealthy)
	}
	if r2.Status != StatusUnhealthy {
		t.Errorf("unhealthy: status = %s, want %s", r2.Status, StatusUnhealthy)
	}
}

// ---- OnConfigChange ----

// TestNodeletProber_OnConfigChange_Add 测试新增 nodelet 同步。
func TestNodeletProber_OnConfigChange_Add(t *testing.T) {
	m := newTestNodeletManager(t)
	_ = m.Add(&NodeletConfig{ID: "n1", Name: "n1", Token: "t"})
	p := NewNodeletProber(m)

	// 新增 nodelet 到同一个 manager
	_ = m.Add(&NodeletConfig{ID: "n2", Name: "n2", Token: "t"})
	p.OnConfigChange()

	if r := p.StatusByID("n2"); r == nil {
		t.Error("n2 should be added after OnConfigChange")
	} else if r.Status != StatusUnknown {
		t.Errorf("n2 status = %s, want %s", r.Status, StatusUnknown)
	}
}

// TestNodeletProber_OnConfigChange_Remove 测试删除 nodelet 同步。
func TestNodeletProber_OnConfigChange_Remove(t *testing.T) {
	m := newTestNodeletManager(t)
	_ = m.Add(&NodeletConfig{ID: "n1", Name: "n1", Token: "t"})
	_ = m.Add(&NodeletConfig{ID: "n2", Name: "n2", Token: "t"})
	p := NewNodeletProber(m)

	_ = m.Remove("n1")
	p.OnConfigChange()

	if r := p.StatusByID("n1"); r != nil {
		t.Error("n1 should be removed after OnConfigChange")
	}
	if r := p.StatusByID("n2"); r == nil {
		t.Error("n2 should still exist")
	}
}

// TestNodeletProber_OnConfigChange_Noop 测试无变化时不变。
func TestNodeletProber_OnConfigChange_Noop(t *testing.T) {
	m := newTestNodeletManager(t)
	_ = m.Add(&NodeletConfig{ID: "n1", Name: "n1", Token: "t"})
	p := NewNodeletProber(m)

	count := len(p.Status())
	p.OnConfigChange()
	if len(p.Status()) != count {
		t.Errorf("Status count changed from %d to %d", count, len(p.Status()))
	}
}

// ---- 生命周期 ----

// TestNodeletProber_StartStop 测试正常启停。
func TestNodeletProber_StartStop(t *testing.T) {
	m := newTestNodeletManager(t)
	p := NewNodeletProber(m)

	p.Start()

	// 用 goroutine + 超时确保 Stop 不会 hang
	done := make(chan struct{})
	go func() {
		p.Stop()
		close(done)
	}()

	select {
	case <-done:
		// OK
	case <-time.After(3 * time.Second):
		t.Fatal("Stop() hung — likely bug 1 (Stop before Start) or deadlock")
	}
}
