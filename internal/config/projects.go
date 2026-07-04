package config

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	runtimestore "oops/internal/store/runtime"
)

// Project 表示一个用户创建的项目，包含多台服务器（Nodelet）。
type Project struct {
	ID                    string    `json:"id"`
	Name                  string    `json:"name"`
	Description           string    `json:"description,omitempty"`
	GitHubRepo            string    `json:"githubRepo,omitempty"`
	NodeletIDs            []string  `json:"nodeletIds"`
	ExcludedContainerRefs []string  `json:"excludedContainerRefs,omitempty"`
	CreatedAt             time.Time `json:"createdAt"`
	UpdatedAt             time.Time `json:"updatedAt"`
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

type projectState struct {
	Projects []Project `json:"projects"`
}

// ProjectStore 提供项目的持久化 CRUD 操作。运行态持久化到 SQLite。
type ProjectStore struct {
	mu      sync.RWMutex
	runtime *runtimestore.Store
	config  projectState
}

// NewProjectStoreWithRuntime loads projects from SQLite.
func NewProjectStoreWithRuntime(runtime *runtimestore.Store) (*ProjectStore, error) {
	if runtime == nil {
		return nil, fmt.Errorf("runtime store is required")
	}
	s := &ProjectStore{runtime: runtime}
	if err := s.load(); err != nil {
		return nil, err
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
	if p.ExcludedContainerRefs == nil {
		p.ExcludedContainerRefs = []string{}
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
	if p.ExcludedContainerRefs == nil {
		p.ExcludedContainerRefs = s.config.Projects[idx].ExcludedContainerRefs
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

// ExcludeContainer 将容器加入项目的排除列表。
func (s *ProjectStore) ExcludeContainer(projectID string, ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, p := range s.config.Projects {
		if p.ID == projectID {
			for _, r := range p.ExcludedContainerRefs {
				if r == ref {
					return fmt.Errorf("container ref %q already excluded from project %q", ref, projectID)
				}
			}
			s.config.Projects[i].ExcludedContainerRefs = append(s.config.Projects[i].ExcludedContainerRefs, ref)
			s.config.Projects[i].UpdatedAt = time.Now()
			return s.saveLocked()
		}
	}
	return fmt.Errorf("project %q not found", projectID)
}

// IncludeContainer 将容器从项目的排除列表移除。
func (s *ProjectStore) IncludeContainer(projectID string, ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, p := range s.config.Projects {
		if p.ID == projectID {
			idx := -1
			for j, r := range p.ExcludedContainerRefs {
				if r == ref {
					idx = j
					break
				}
			}
			if idx < 0 {
				return fmt.Errorf("container ref %q not found in project %q exclusions", ref, projectID)
			}
			s.config.Projects[i].ExcludedContainerRefs = append(p.ExcludedContainerRefs[:idx], p.ExcludedContainerRefs[idx+1:]...)
			s.config.Projects[i].UpdatedAt = time.Now()
			return s.saveLocked()
		}
	}
	return fmt.Errorf("project %q not found", projectID)
}

func (s *ProjectStore) saveLocked() error {
	return s.runtime.ReplaceProjects(context.Background(), projectsToRuntime(s.config.Projects))
}

func (s *ProjectStore) load() error {
	ctx := context.Background()
	records, err := s.runtime.ListProjects(ctx)
	if err != nil {
		return err
	}
	s.config.Projects = projectsFromRuntime(records)
	s.ensureDefaults()
	return nil
}

func (s *ProjectStore) ensureDefaults() {
	if s.config.Projects == nil {
		s.config.Projects = []Project{}
	}
	for i := range s.config.Projects {
		if s.config.Projects[i].NodeletIDs == nil {
			s.config.Projects[i].NodeletIDs = []string{}
		}
		if s.config.Projects[i].ExcludedContainerRefs == nil {
			s.config.Projects[i].ExcludedContainerRefs = []string{}
		}
	}
}

func projectsToRuntime(projects []Project) []runtimestore.ProjectRecord {
	records := make([]runtimestore.ProjectRecord, len(projects))
	for i, p := range projects {
		records[i] = runtimestore.ProjectRecord{
			ID:                    p.ID,
			Name:                  p.Name,
			Description:           p.Description,
			GitHubRepo:            p.GitHubRepo,
			NodeletIDs:            append([]string{}, p.NodeletIDs...),
			ExcludedContainerRefs: append([]string{}, p.ExcludedContainerRefs...),
			CreatedAt:             p.CreatedAt,
			UpdatedAt:             p.UpdatedAt,
		}
	}
	return records
}

func projectsFromRuntime(records []runtimestore.ProjectRecord) []Project {
	projects := make([]Project, len(records))
	for i, r := range records {
		projects[i] = Project{
			ID:                    r.ID,
			Name:                  r.Name,
			Description:           r.Description,
			GitHubRepo:            r.GitHubRepo,
			NodeletIDs:            append([]string{}, r.NodeletIDs...),
			ExcludedContainerRefs: append([]string{}, r.ExcludedContainerRefs...),
			CreatedAt:             r.CreatedAt,
			UpdatedAt:             r.UpdatedAt,
		}
	}
	return projects
}
