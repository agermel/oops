package project

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	runtimestore "oops/internal/store/runtime"
)

// Project 表示一个用户创建的项目，包含多台服务器。
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

// Store 管理项目领域数据，并将每次变更提交到运行时存储。
type Store struct {
	mu      sync.RWMutex
	runtime *runtimestore.Store
}

// NewStore 创建项目存储。
func NewStore(runtime *runtimestore.Store) (*Store, error) {
	if runtime == nil {
		return nil, fmt.Errorf("runtime store is required")
	}
	return &Store{runtime: runtime}, nil
}

// List 返回按创建时间和 ID 排序的深拷贝快照。
func (s *Store) List() []Project {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.runtime.ListProjects(context.Background())
	if err != nil {
		return []Project{}
	}
	projects := make([]Project, len(rows))
	for i, row := range rows {
		projects[i] = projectFromRecord(row)
	}
	return projects
}

// Get 按 ID 返回项目的深拷贝，未找到时返回 nil。
func (s *Store) Get(id string) *Project {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row, err := s.runtime.GetProject(context.Background(), id)
	if err != nil || row == nil {
		return nil
	}
	project := projectFromRecord(*row)
	return &project
}

// Add 创建项目并由服务端写入时间戳。
func (s *Store) Add(project Project) error {
	project = normalize(project)
	if err := validate(project); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, err := s.runtime.GetProject(context.Background(), project.ID); err != nil {
		return err
	} else if existing != nil {
		return fmt.Errorf("project %q already exists", project.ID)
	}
	if err := s.ensureUniqueNameLocked(project.ID, project.Name); err != nil {
		return err
	}

	now := time.Now()
	project.CreatedAt = now
	project.UpdatedAt = now
	project.NodeletIDs = emptyIfNil(project.NodeletIDs)
	project.ExcludedContainerRefs = emptyIfNil(project.ExcludedContainerRefs)
	return s.runtime.CreateProject(context.Background(), projectRecord(project))
}

// Update 更新项目标量；nil 集合保留现值，显式集合在同一事务内替换。
func (s *Store) Update(project Project) error {
	project = normalize(project)
	if err := validate(project); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, err := s.runtime.GetProject(context.Background(), project.ID)
	if err != nil {
		return err
	}
	if existing == nil {
		return fmt.Errorf("project %q not found", project.ID)
	}
	if err := s.ensureUniqueNameLocked(project.ID, project.Name); err != nil {
		return err
	}

	project.CreatedAt = existing.CreatedAt
	project.UpdatedAt = time.Now()
	if project.NodeletIDs == nil {
		project.NodeletIDs = slices.Clone(existing.NodeletIDs)
	}
	if project.ExcludedContainerRefs == nil {
		project.ExcludedContainerRefs = slices.Clone(existing.ExcludedContainerRefs)
	}
	return s.runtime.UpdateProject(context.Background(), projectRecord(project))
}

// Remove 删除项目。
func (s *Store) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runtime.DeleteProject(context.Background(), id)
}

// AddNodelet 向项目添加服务器。
func (s *Store) AddNodelet(projectID, nodeletID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	project, err := s.projectLocked(projectID)
	if err != nil {
		return err
	}
	if slices.Contains(project.NodeletIDs, nodeletID) {
		return fmt.Errorf("nodelet %q already in project %q", nodeletID, projectID)
	}
	project.NodeletIDs = append(project.NodeletIDs, nodeletID)
	project.UpdatedAt = time.Now()
	return s.runtime.UpdateProject(context.Background(), projectRecord(*project))
}

// RemoveNodelet 从项目中移除服务器。
func (s *Store) RemoveNodelet(projectID, nodeletID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	project, err := s.projectLocked(projectID)
	if err != nil {
		return err
	}
	index := slices.Index(project.NodeletIDs, nodeletID)
	if index < 0 {
		return fmt.Errorf("nodelet %q not found in project %q", nodeletID, projectID)
	}
	project.NodeletIDs = slices.Delete(project.NodeletIDs, index, index+1)
	project.UpdatedAt = time.Now()
	return s.runtime.UpdateProject(context.Background(), projectRecord(*project))
}

// ExcludeContainer 将容器加入项目排除列表。
func (s *Store) ExcludeContainer(projectID, ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	project, err := s.projectLocked(projectID)
	if err != nil {
		return err
	}
	if slices.Contains(project.ExcludedContainerRefs, ref) {
		return fmt.Errorf("container ref %q already excluded from project %q", ref, projectID)
	}
	project.ExcludedContainerRefs = append(project.ExcludedContainerRefs, ref)
	project.UpdatedAt = time.Now()
	return s.runtime.UpdateProject(context.Background(), projectRecord(*project))
}

// IncludeContainer 从项目排除列表移除容器。
func (s *Store) IncludeContainer(projectID, ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	project, err := s.projectLocked(projectID)
	if err != nil {
		return err
	}
	index := slices.Index(project.ExcludedContainerRefs, ref)
	if index < 0 {
		return fmt.Errorf("container ref %q not found in project %q exclusions", ref, projectID)
	}
	project.ExcludedContainerRefs = slices.Delete(project.ExcludedContainerRefs, index, index+1)
	project.UpdatedAt = time.Now()
	return s.runtime.UpdateProject(context.Background(), projectRecord(*project))
}

func (s *Store) projectLocked(id string) (*Project, error) {
	row, err := s.runtime.GetProject(context.Background(), id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, fmt.Errorf("project %q not found", id)
	}
	project := projectFromRecord(*row)
	return &project, nil
}

func (s *Store) ensureUniqueNameLocked(id, name string) error {
	projects, err := s.runtime.ListProjects(context.Background())
	if err != nil {
		return err
	}
	for _, existing := range projects {
		if existing.ID != id && existing.Name == name {
			return fmt.Errorf("project name %q already exists", name)
		}
	}
	return nil
}

func validate(project Project) error {
	if project.ID == "" {
		return fmt.Errorf("project id is required")
	}
	if project.Name == "" {
		return fmt.Errorf("project name is required")
	}
	return nil
}

func normalize(project Project) Project {
	project.ID = strings.TrimSpace(project.ID)
	project.Name = strings.TrimSpace(project.Name)
	project.Description = strings.TrimSpace(project.Description)
	project.GitHubRepo = strings.TrimSpace(project.GitHubRepo)
	project.NodeletIDs = slices.Clone(project.NodeletIDs)
	project.ExcludedContainerRefs = slices.Clone(project.ExcludedContainerRefs)
	return project
}

func emptyIfNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func projectRecord(project Project) runtimestore.ProjectRecord {
	return runtimestore.ProjectRecord{
		ID:                    project.ID,
		Name:                  project.Name,
		Description:           project.Description,
		GitHubRepo:            project.GitHubRepo,
		NodeletIDs:            slices.Clone(project.NodeletIDs),
		ExcludedContainerRefs: slices.Clone(project.ExcludedContainerRefs),
		CreatedAt:             project.CreatedAt,
		UpdatedAt:             project.UpdatedAt,
	}
}

func projectFromRecord(row runtimestore.ProjectRecord) Project {
	return Project{
		ID:                    row.ID,
		Name:                  row.Name,
		Description:           row.Description,
		GitHubRepo:            row.GitHubRepo,
		NodeletIDs:            emptyIfNil(slices.Clone(row.NodeletIDs)),
		ExcludedContainerRefs: emptyIfNil(slices.Clone(row.ExcludedContainerRefs)),
		CreatedAt:             row.CreatedAt,
		UpdatedAt:             row.UpdatedAt,
	}
}
