package api

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"oops/internal/connection"
	"oops/internal/llm"
	"oops/internal/nodelet"
)

// ListNodelets 实现 llm.OpsData，返回所有 Nodelet 概要。
// 先读 Prober 缓存判断可用性，仅对健康节点实时获取 Docker 详情。
func (s *Server) ListNodelets(ctx context.Context) ([]llm.NodeletSummary, error) {
	nodelets := s.nodeletManager.List()
	results := make([]llm.NodeletSummary, len(nodelets))
	var wg sync.WaitGroup
	for index, item := range nodelets {
		wg.Add(1)
		go func(index int, item nodelet.NodeletConfig) {
			defer wg.Done()

			summary := llm.NodeletSummary{
				ID:      item.ID,
				Name:    item.Name,
				Address: item.Address,
			}

			// 从 Prober 缓存获取可用性
			if s.nodeletProber != nil {
				pr := s.nodeletProber.StatusByID(item.ID)
				if pr != nil && pr.Status == nodelet.StatusHealthy {
					summary.Available = true
				} else if pr != nil {
					summary.Error = pr.LastError
					results[index] = summary
					return
				}
			}

			// 仅健康节点实时获取 Docker 详情
			checkCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
			defer cancel()

			host, err := s.nodeletClient.Host(checkCtx, item.Address, item.Token)
			if err != nil {
				summary.Error = err.Error()
				summary.Available = false
			} else {
				summary.Available = true
				summary.DockerVersion = host.DockerVersion
				summary.Runtime = host.Runtime
				summary.NCPU = host.NCPU
				summary.MemTotal = host.MemTotal
			}
			results[index] = summary
		}(index, item)
	}
	wg.Wait()
	return results, nil
}

// ListContainers 实现 llm.OpsData，返回指定 Nodelet 的容器列表。
// status 不为空时仅返回匹配状态的容器。
func (s *Server) ListContainers(ctx context.Context, nodeletID, status string) ([]nodelet.Container, error) {
	item, ok := s.findNodelet(nodeletID)
	if !ok {
		return nil, fmt.Errorf("nodelet %q not found", nodeletID)
	}
	containers, err := s.nodeletClient.Containers(ctx, item.Address, item.Token)
	if err != nil {
		return nil, err
	}
	if status == "" {
		return containers, nil
	}
	filtered := make([]nodelet.Container, 0, len(containers))
	for _, c := range containers {
		if c.State == status {
			filtered = append(filtered, c)
		}
	}
	return filtered, nil
}

// GetLogs 实现 llm.OpsData，返回指定容器的历史日志。
func (s *Server) GetLogs(ctx context.Context, nodeletID, containerID string, tail int) ([]nodelet.LogEntry, error) {
	item, ok := s.findNodelet(nodeletID)
	if !ok {
		return nil, fmt.Errorf("nodelet %q not found", nodeletID)
	}
	return s.nodeletClient.ContainerLogs(ctx, item.Address, item.Token, containerID, strconv.Itoa(tail))
}

// CheckConnections 实现 llm.OpsData，执行所有连接健康检查。
func (s *Server) CheckConnections(ctx context.Context) ([]llm.ConnectionStatus, error) {
	results := make([]llm.ConnectionStatus, len(s.connections))
	var wg sync.WaitGroup
	for index, conn := range s.connections {
		wg.Add(1)
		go func(index int, conn connection.Connection) {
			defer wg.Done()

			checkCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
			defer cancel()

			result, err := s.registry.Check(checkCtx, conn)
			cs := llm.ConnectionStatus{
				ID:      conn.ID,
				Name:    conn.Name,
				Type:    conn.Type,
				Address: conn.Address,
				Status:  string(result.Status),
				Message: result.Message,
				Latency: result.Latency,
			}
			if err != nil {
				cs.Error = err.Error()
				cs.Status = string(connection.StatusUnknown)
			}
			results[index] = cs
		}(index, conn)
	}
	wg.Wait()
	return results, nil
}

