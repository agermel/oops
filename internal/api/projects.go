package api

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"oops/internal/docker"
	"oops/internal/mcp"
	"oops/internal/nodelet"
	"oops/internal/project"

	"github.com/google/uuid"
)

// serverWithNodelet 是项目详情中一台服务器的聚合视图。
type serverWithNodelet struct {
	Nodelet nodelet.NodeletConfig `json:"nodelet"`
	Host    nodeletHostSummary    `json:"host"`
	Error   string                `json:"error,omitempty"`
}

type nodeletHostSummary struct {
	Available     bool   `json:"available"`
	DockerVersion string `json:"dockerVersion"`
	Runtime       string `json:"runtime"`
	NCPU          int    `json:"nCPU"`
	MemTotal      int64  `json:"memTotal"`
}

// projectPayload keeps HTTP field-presence semantics at the API boundary.
// A nil collection represents an omitted or null JSON field.
type projectPayload struct {
	ID                    string    `json:"id"`
	Name                  string    `json:"name"`
	Description           string    `json:"description,omitempty"`
	GitHubRepo            string    `json:"githubRepo,omitempty"`
	NodeletIDs            *[]string `json:"nodeletIds"`
	ExcludedContainerRefs *[]string `json:"excludedContainerRefs"`
}

func (p projectPayload) project(id string) project.Project {
	return project.Project{
		ID:                    id,
		Name:                  p.Name,
		Description:           p.Description,
		GitHubRepo:            p.GitHubRepo,
		NodeletIDs:            cloneStrings(p.NodeletIDs),
		ExcludedContainerRefs: cloneStrings(p.ExcludedContainerRefs),
	}
}

func cloneStrings(values *[]string) []string {
	if values == nil {
		return nil
	}
	return slices.Clone(*values)
}

// containerWithType 是带服务类型识别的容器列表项。
type containerWithType struct {
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	Image       string                `json:"image"`
	Command     string                `json:"command,omitempty"`
	State       string                `json:"state"`
	Status      string                `json:"status,omitempty"`
	Health      string                `json:"health,omitempty"`
	Ports       []nodelet.PortMapping `json:"ports,omitempty"`
	ServiceType string                `json:"serviceType"`
}

func projectErrorStatus(err error) int {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "is required"):
		return http.StatusBadRequest
	case strings.Contains(msg, "already exists") || strings.Contains(msg, "already in project"):
		return http.StatusConflict
	case strings.Contains(msg, "not found"):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

// handleProjectList handles GET /api/projects.
func (s *Server) handleProjectList(w http.ResponseWriter, r *http.Request) {
	if s.projectStore == nil {
		writeJSON(w, []project.Project{})
		return
	}
	writeJSON(w, s.projectStore.List())
}

// handleProjectCreate handles POST /api/projects.
func (s *Server) handleProjectCreate(w http.ResponseWriter, r *http.Request) {
	if s.projectStore == nil {
		writeJSONError(w, "project store not initialized", http.StatusServiceUnavailable)
		return
	}
	var payload projectPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}
	p := payload.project(strings.TrimSpace(payload.ID))
	p.ID = strings.TrimSpace(p.ID)
	if p.ID == "" {
		p.ID = "project-" + uuid.NewString()
	}
	p.Name = strings.TrimSpace(p.Name)
	p.Description = strings.TrimSpace(p.Description)
	p.GitHubRepo = strings.TrimSpace(p.GitHubRepo)
	if p.Name == "" {
		writeJSONError(w, "project name is required", http.StatusBadRequest)
		return
	}
	if err := s.projectStore.Add(p); err != nil {
		writeJSONError(w, err.Error(), projectErrorStatus(err))
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, s.projectStore.Get(p.ID))
}

// handleProjectGet handles GET /api/projects/{pid}.
func (s *Server) handleProjectGet(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("pid")
	if s.projectStore == nil {
		writeJSONError(w, "not found", http.StatusNotFound)
		return
	}
	p := s.projectStore.Get(projectID)
	if p == nil {
		writeJSONError(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, p)
}

// handleProjectUpdate handles PUT /api/projects/{pid}.
func (s *Server) handleProjectUpdate(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("pid")
	if s.projectStore == nil {
		writeJSONError(w, "project store not initialized", http.StatusServiceUnavailable)
		return
	}
	var payload projectPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}
	p := payload.project(projectID)
	p.Name = strings.TrimSpace(p.Name)
	p.Description = strings.TrimSpace(p.Description)
	p.GitHubRepo = strings.TrimSpace(p.GitHubRepo)
	if p.Name == "" {
		writeJSONError(w, "project name is required", http.StatusBadRequest)
		return
	}
	if err := s.projectStore.Update(p); err != nil {
		writeJSONError(w, err.Error(), projectErrorStatus(err))
		return
	}
	writeJSON(w, s.projectStore.Get(projectID))
}

// handleProjectDelete handles DELETE /api/projects/{pid}.
func (s *Server) handleProjectDelete(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("pid")
	if s.projectStore == nil {
		writeJSONError(w, "project store not initialized", http.StatusServiceUnavailable)
		return
	}
	if err := s.projectStore.Remove(projectID); err != nil {
		writeJSONError(w, err.Error(), projectErrorStatus(err))
		return
	}
	writeJSONOK(w)
}

// handleProjectServersList handles GET /api/projects/{pid}/servers.
func (s *Server) handleProjectServersList(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("pid")
	s.listProjectServers(w, r, projectID)
}

// handleProjectServersAdd handles POST /api/projects/{pid}/servers.
func (s *Server) handleProjectServersAdd(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("pid")
	s.addProjectServer(w, r, projectID)
}

func (s *Server) listProjectServers(w http.ResponseWriter, r *http.Request, projectID string) {
	if s.projectStore == nil {
		writeJSON(w, []serverWithNodelet{})
		return
	}

	p := s.projectStore.Get(projectID)
	if p == nil {
		writeJSONError(w, "not found", http.StatusNotFound)
		return
	}

	results := make([]serverWithNodelet, len(p.NodeletIDs))
	for i, nid := range p.NodeletIDs {
		item, ok := s.findNodelet(nid)
		sw := serverWithNodelet{
			Nodelet: nodelet.NodeletConfig{Name: nid},
		}
		if ok {
			sw.Nodelet = item
			// 从 Prober 缓存获取连通性状态
			if s.nodeletProber != nil {
				pr := s.nodeletProber.StatusByID(nid)
				if pr != nil {
					sw.Host.Available = pr.Status == nodelet.StatusHealthy
					if pr.LastError != "" {
						sw.Error = pr.LastError
					}
				}
			}
		} else {
			sw.Error = "nodelet not found in config"
		}
		results[i] = sw
	}

	writeJSON(w, results)
}

func (s *Server) addProjectServer(w http.ResponseWriter, r *http.Request, projectID string) {
	if s.projectStore == nil {
		writeJSONError(w, "project store not initialized", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		NodeletID string `json:"nodeletId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.NodeletID == "" {
		writeJSONError(w, "nodeletId is required", http.StatusBadRequest)
		return
	}

	// 验证 nodelet 存在。
	if _, ok := s.findNodelet(req.NodeletID); !ok {
		writeJSONError(w, "nodelet not found in config", http.StatusBadRequest)
		return
	}

	if err := s.projectStore.AddNodelet(projectID, req.NodeletID); err != nil {
		writeJSONError(w, err.Error(), projectErrorStatus(err))
		return
	}
	writeJSONOK(w)
}

// handleProjectContainers handles GET /api/projects/{pid}/servers/{sid}/containers.
func (s *Server) handleProjectContainers(w http.ResponseWriter, r *http.Request) {
	nodeletID := r.PathValue("sid")

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	item, ok := s.findNodelet(nodeletID)
	if !ok {
		writeJSONError(w, "not found", http.StatusNotFound)
		return
	}

	containers, err := s.nodeletClient.Containers(ctx, item.Address, item.Token)
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	result := make([]containerWithType, len(containers))
	for i, c := range containers {
		stype := docker.DetectServiceType(c.Image)
		result[i] = containerWithType{
			ID:          c.ID,
			Name:        c.Name,
			Image:       c.Image,
			Command:     c.Command,
			State:       c.State,
			Status:      c.Status,
			Health:      c.Health,
			Ports:       c.Ports,
			ServiceType: string(stype),
		}
	}

	writeJSON(w, result)
}

// handleProjectExcludeContainer handles POST /api/projects/{pid}/excluded-containers.
func (s *Server) handleProjectExcludeContainer(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("pid")

	var body struct {
		NodeletID   string `json:"nodeletId"`
		ContainerID string `json:"containerId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.NodeletID == "" || body.ContainerID == "" {
		writeJSONError(w, "nodeletId and containerId are required", http.StatusBadRequest)
		return
	}

	ref := body.NodeletID + "/" + body.ContainerID
	if err := s.projectStore.ExcludeContainer(projectID, ref); err != nil {
		writeJSONError(w, err.Error(), projectErrorStatus(err))
		return
	}
	writeJSONOK(w)
}

// handleProjectIncludeContainer handles DELETE /api/projects/{pid}/excluded-containers.
func (s *Server) handleProjectIncludeContainer(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("pid")
	nodeletID := r.URL.Query().Get("nodeletId")
	containerID := r.URL.Query().Get("containerId")

	if nodeletID == "" || containerID == "" {
		writeJSONError(w, "nodeletId and containerId query params are required", http.StatusBadRequest)
		return
	}

	ref := nodeletID + "/" + containerID
	if err := s.projectStore.IncludeContainer(projectID, ref); err != nil {
		writeJSONError(w, err.Error(), projectErrorStatus(err))
		return
	}
	writeJSONOK(w)
}

// ---- Project MCP ----

// projectMCPScope 标识项目 MCP 连接的绑定粒度。
type projectMCPScope string

const (
	mcpScopeContainer projectMCPScope = "container"
	mcpScopeNodelet   projectMCPScope = "nodelet"
)

// projectMCPConnection 是带 scope 注解的项目级 MCP 连接。
type projectMCPConnection struct {
	mcp.ConnectionWithStatus
	Scope projectMCPScope `json:"scope"`
}

// handleProjectMCPList handles GET /api/projects/{pid}/mcp/connections.
func (s *Server) handleProjectMCPList(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("pid")

	if s.projectStore == nil {
		writeJSONError(w, "project store not initialized", http.StatusServiceUnavailable)
		return
	}
	if s.mcpManager == nil {
		writeJSON(w, []projectMCPConnection{})
		return
	}

	p := s.projectStore.Get(projectID)
	if p == nil {
		writeJSONError(w, "not found", http.StatusNotFound)
		return
	}

	nodeletSet := make(map[string]struct{}, len(p.NodeletIDs))
	for _, nid := range p.NodeletIDs {
		nodeletSet[nid] = struct{}{}
	}

	result := make([]projectMCPConnection, 0)
	for _, conn := range s.decorateMCPConnections(r.Context(), s.mcpManager.List()) {
		if conn.NodeletID == "" {
			continue
		}
		if _, ok := nodeletSet[conn.NodeletID]; !ok {
			continue
		}
		scope := mcpScopeNodelet
		if conn.ContainerID != "" {
			scope = mcpScopeContainer
		}
		result = append(result, projectMCPConnection{
			ConnectionWithStatus: conn,
			Scope:                scope,
		})
	}

	writeJSON(w, result)
}
