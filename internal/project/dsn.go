package project

import (
	"context"
	"fmt"
	"maps"

	runtimestore "oops/internal/store/runtime"
)

// DSNStore 管理容器级 DSN 覆盖值。
type DSNStore struct {
	runtime *runtimestore.Store
}

// NewDSNStore 创建 DSN 覆盖值存储。
func NewDSNStore(runtime *runtimestore.Store) (*DSNStore, error) {
	if runtime == nil {
		return nil, fmt.Errorf("runtime store is required")
	}
	return &DSNStore{runtime: runtime}, nil
}

// Get 返回容器 DSN 覆盖值的深拷贝，未设置时返回 nil。
func (s *DSNStore) Get(nodeletID, containerID string) map[string]string {
	row, err := s.runtime.GetDSNRecord(context.Background(), nodeletID, containerID)
	if err != nil || row == nil {
		return nil
	}
	return maps.Clone(row.Pairs)
}

// Set 以单容器事务替换整组 DSN 覆盖值。
func (s *DSNStore) Set(nodeletID, containerID string, pairs map[string]string) error {
	if len(pairs) == 0 {
		return s.Delete(nodeletID, containerID)
	}
	return s.runtime.SetDSNRecord(context.Background(), runtimestore.DSNRecord{
		NodeletID:   nodeletID,
		ContainerID: containerID,
		Pairs:       maps.Clone(pairs),
	})
}

// Delete 删除容器 DSN 覆盖值。
func (s *DSNStore) Delete(nodeletID, containerID string) error {
	return s.runtime.DeleteDSNRecord(context.Background(), nodeletID, containerID)
}
