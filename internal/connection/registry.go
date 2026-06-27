package connection

import (
	"context"
	"fmt"
	"sync"
)

// Registry 保存连接类型到 Checker 的映射。
type Registry struct {
	mu       sync.RWMutex
	checkers map[string]Checker
}

// NewRegistry 创建一个空的 Checker 注册表。
func NewRegistry() *Registry {
	return &Registry{checkers: map[string]Checker{}}
}

// Register 注册一种连接类型的 Checker。
func (r *Registry) Register(checker Checker) error {
	if checker == nil {
		return fmt.Errorf("checker is required")
	}
	connectionType := checker.Type()
	if connectionType == "" {
		return fmt.Errorf("connection type is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.checkers[connectionType]; exists {
		return fmt.Errorf("checker for %q already registered", connectionType)
	}
	r.checkers[connectionType] = checker
	return nil
}

// Get 按连接类型查找 Checker。
func (r *Registry) Get(connectionType string) (Checker, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	checker, ok := r.checkers[connectionType]
	return checker, ok
}

// Check 根据 Connection.Type 找到对应 Checker 并执行探测。
func (r *Registry) Check(ctx context.Context, conn Connection) (Result, error) {
	checker, ok := r.Get(conn.Type)
	if !ok {
		return Result{}, fmt.Errorf("checker for %q not found", conn.Type)
	}
	return checker.Check(ctx, conn), nil
}
