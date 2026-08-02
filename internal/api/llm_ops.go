package api

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"oops/internal/agent/runtime/tools/platform"
	"oops/internal/nodelet"
)

// ListNodelets 实现 platform.OpsData，返回所有 Nodelet 概要。
// 先读 Prober 缓存判断可用性，仅对健康节点实时获取 Docker 详情。
func (s *Server) ListNodelets(ctx context.Context) ([]platform.NodeletSummary, error) {
	nodelets := s.nodeletManager.List()
	results := make([]platform.NodeletSummary, len(nodelets))
	var wg sync.WaitGroup
	for index, item := range nodelets {
		wg.Add(1)
		go func(index int, item nodelet.NodeletConfig) {
			defer wg.Done()

			summary := platform.NodeletSummary{
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

// ListContainers 实现 platform.OpsData，返回指定 Nodelet 的容器列表。
// status 不为空时仅返回匹配状态的容器。
// projectID 不为空时会过滤掉项目已隐藏的容器（excludedContainerRefs）。
func (s *Server) ListContainers(ctx context.Context, projectID, nodeletID, status string) ([]nodelet.Container, error) {
	item, ok := s.findNodelet(nodeletID)
	if !ok {
		return nil, fmt.Errorf("nodelet %q not found", nodeletID)
	}
	containers, err := s.nodeletClient.Containers(ctx, item.Address, item.Token)
	if err != nil {
		return nil, err
	}

	// 过滤项目已隐藏的容器。
	if projectID != "" && s.projectStore != nil {
		if p := s.projectStore.Get(projectID); p != nil {
			excluded := make(map[string]bool, len(p.ExcludedContainerRefs))
			for _, ref := range p.ExcludedContainerRefs {
				excluded[ref] = true
			}
			if len(excluded) > 0 {
				filtered := make([]nodelet.Container, 0, len(containers))
				for _, c := range containers {
					ref := nodeletID + "/" + c.ID
					if !excluded[ref] {
						filtered = append(filtered, c)
					}
				}
				containers = filtered
			}
		}
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

// GetLogs 实现 platform.OpsData，返回指定容器的历史日志。
func (s *Server) GetLogs(ctx context.Context, nodeletID, containerID string, tail int) ([]nodelet.LogEntry, error) {
	item, ok := s.findNodelet(nodeletID)
	if !ok {
		return nil, fmt.Errorf("nodelet %q not found", nodeletID)
	}
	return s.nodeletClient.ContainerLogs(ctx, item.Address, item.Token, containerID, strconv.Itoa(tail))
}

// GetProjectRepo 实现 platform.OpsData，返回项目的 GitHub 仓库 URL。
func (s *Server) GetProjectRepo(ctx context.Context, projectID string) (string, error) {
	if s.projectStore == nil {
		return "", fmt.Errorf("project store not initialized")
	}
	p := s.projectStore.Get(projectID)
	if p == nil {
		return "", fmt.Errorf("project %q not found", projectID)
	}
	if p.GitHubRepo == "" {
		return "", fmt.Errorf("project %q 未配置 GitHub 仓库", p.Name)
	}
	return p.GitHubRepo, nil
}

// ContainerExec 实现 platform.OpsData，在指定容器内执行命令。
func (s *Server) ContainerExec(ctx context.Context, nodeletID, containerID string, cmd []string) (nodelet.ExecResult, error) {
	item, ok := s.findNodelet(nodeletID)
	if !ok {
		return nodelet.ExecResult{}, fmt.Errorf("nodelet %q not found", nodeletID)
	}
	execCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return s.nodeletClient.ContainerExec(execCtx, item.Address, item.Token, containerID, cmd)
}
