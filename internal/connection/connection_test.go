package connection

import (
	"context"
	"testing"
	"time"
)

// fakeChecker 是测试用 Checker，固定返回 alive。
type fakeChecker struct{}

// Type 返回测试连接类型。
func (fakeChecker) Type() string {
	return "fake"
}

// Check 返回一个成功的探测结果。
func (fakeChecker) Check(_ context.Context, conn Connection) Result {
	return Result{
		ConnectionID: conn.ID,
		Status:       StatusAlive,
		CheckedAt:    time.Now(),
	}
}

// TestRegistryCheck 验证 Registry 能按连接类型分发到对应 Checker。
func TestRegistryCheck(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(fakeChecker{}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	result, err := registry.Check(context.Background(), Connection{
		ID:   "conn-1",
		Type: "fake",
	})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if result.Status != StatusAlive {
		t.Fatalf("Status = %q, want %q", result.Status, StatusAlive)
	}
}
