package config

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"oops/internal/store"
)

// DefaultProjectsPath 是项目配置文件的默认路径。
const DefaultProjectsPath = "config/projects.json"

// Project 表示一个用户创建的项目，包含多台服务器（Nodelet）。
type Project struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	NodeletIDs  []string  `json:"nodeletIds"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// UnmarshalJSON 实现自定义反序列化，兼容前端发送的空字符串时间字段。
func (p *Project) UnmarshalJSON(data []byte) error {
	type Alias Project
	aux := struct {
		CreatedAt string `json:"createdAt"`
		UpdatedAt string `json:"updatedAt"`
		*Alias
	}{
		Alias: (*Alias)(p),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.CreatedAt != "" {
		t, err := time.Parse(time.RFC3339, aux.CreatedAt)
		if err != nil {
			return fmt.Errorf("invalid createdAt: %w", err)
		}
		p.CreatedAt = t
	}
	if aux.UpdatedAt != "" {
		t, err := time.Parse(time.RFC3339, aux.UpdatedAt)
		if err != nil {
			return fmt.Errorf("invalid updatedAt: %w", err)
		}
		p.UpdatedAt = t
	}
	return nil
}

// ProjectStoreConfig 是项目存储文件的顶层结构。
type ProjectStoreConfig struct {
	Projects []Project `json:"projects"`
}

// ProjectStore 提供项目的持久化 CRUD 操作。零值不可用，使用 NewProjectStore 创建。
type ProjectStore struct {
	mu     sync.RWMutex
	path   string
	config ProjectStoreConfig
}

// NewProjectStore 从指定路径加载项目存储。如果文件不存在则创建空存储。
func NewProjectStore(path string) (*ProjectStore, error) {
	s := &ProjectStore{path: path}
	if err := store.LoadJSON(path, &s.config); err != nil {
		return nil, err
	}
	if s.config.Projects == nil {
		s.config.Projects = []Project{}
	}
	return s, nil
}

// List 返回所有项目的副本。
func (s *ProjectStore) List() []Project {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Project, len(s.config.Projects))
	copy(result, s.config.Projects)
	return result
}

// Get 按 ID 查找项目，未找到返回 nil。
func (s *ProjectStore) Get(id string) *Project {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for i := range s.config.Projects {
		if s.config.Projects[i].ID == id {
			p := s.config.Projects[i]
			return &p
		}
	}
	return nil
}

// Add 创建新项目并持久化。
func (s *ProjectStore) Add(p Project) error {
	if p.ID == "" {
		return fmt.Errorf("project id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, existing := range s.config.Projects {
		if existing.ID == p.ID {
			return fmt.Errorf("project %q already exists", p.ID)
		}
	}

	now := time.Now()
	p.CreatedAt = now
	p.UpdatedAt = now
	if p.NodeletIDs == nil {
		p.NodeletIDs = []string{}
	}

	s.config.Projects = append(s.config.Projects, p)
	return s.saveLocked()
}

// Update 更新已有项目并持久化。
func (s *ProjectStore) Update(p Project) error {
	if p.ID == "" {
		return fmt.Errorf("project id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	idx := -1
	for i, existing := range s.config.Projects {
		if existing.ID == p.ID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("project %q not found", p.ID)
	}

	p.CreatedAt = s.config.Projects[idx].CreatedAt
	p.UpdatedAt = time.Now()
	if p.NodeletIDs == nil {
		p.NodeletIDs = s.config.Projects[idx].NodeletIDs
	}

	s.config.Projects[idx] = p
	return s.saveLocked()
}

// Remove 删除项目并持久化。
func (s *ProjectStore) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := -1
	for i, existing := range s.config.Projects {
		if existing.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("project %q not found", id)
	}

	s.config.Projects = append(s.config.Projects[:idx], s.config.Projects[idx+1:]...)
	return s.saveLocked()
}

// AddNodelet 向项目添加一台服务器。
func (s *ProjectStore) AddNodelet(projectID string, nodeletID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, p := range s.config.Projects {
		if p.ID == projectID {
			for _, nid := range p.NodeletIDs {
				if nid == nodeletID {
					return fmt.Errorf("nodelet %q already in project %q", nodeletID, projectID)
				}
			}
			s.config.Projects[i].NodeletIDs = append(s.config.Projects[i].NodeletIDs, nodeletID)
			s.config.Projects[i].UpdatedAt = time.Now()
			return s.saveLocked()
		}
	}
	return fmt.Errorf("project %q not found", projectID)
}

// RemoveNodelet 从项目中移除一台服务器。
func (s *ProjectStore) RemoveNodelet(projectID string, nodeletID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, p := range s.config.Projects {
		if p.ID == projectID {
			idx := -1
			for j, nid := range p.NodeletIDs {
				if nid == nodeletID {
					idx = j
					break
				}
			}
			if idx < 0 {
				return fmt.Errorf("nodelet %q not found in project %q", nodeletID, projectID)
			}
			s.config.Projects[i].NodeletIDs = append(p.NodeletIDs[:idx], p.NodeletIDs[idx+1:]...)
			s.config.Projects[i].UpdatedAt = time.Now()
			return s.saveLocked()
		}
	}
	return fmt.Errorf("project %q not found", projectID)
}

func (s *ProjectStore) saveLocked() error {
	return store.SaveJSON(s.path, s.config)
}
