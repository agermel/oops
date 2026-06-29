package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"oops/internal/config"
	"oops/internal/docker"
)

// serverWithNodelet 是项目详情中一台服务器的聚合视图。
type serverWithNodelet struct {
	Nodelet config.NodeletConfig `json:"nodelet"`
	Host    nodeletHostSummary   `json:"host"`
	Error   string               `json:"error,omitempty"`
}

type nodeletHostSummary struct {
	Available     bool   `json:"available"`
	DockerVersion string `json:"dockerVersion"`
	Runtime       string `json:"runtime"`
	NCPU          int    `json:"nCPU"`
	MemTotal      int64  `json:"memTotal"`
}

// containerWithType 是带服务类型识别的容器列表项。
type containerWithType struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Image       string `json:"image"`
	State       string `json:"state"`
	Health      string `json:"health,omitempty"`
	ServiceType string `json:"serviceType"`
}

// handleProjectList handles GET /api/projects.
func (s *Server) handleProjectList(w http.ResponseWriter, r *http.Request) {
	if s.projectStore == nil {
		writeJSON(w, []config.Project{})
		return
	}
	writeJSON(w, s.projectStore.List())
}

// handleProjectCreate handles POST /api/projects.
func (s *Server) handleProjectCreate(w http.ResponseWriter, r *http.Request) {
	if s.projectStore == nil {
		http.Error(w, `{"error":"project store not initialized"}`, http.StatusServiceUnavailable)
		return
	}
	var p config.Project
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if err := s.projectStore.Add(p); err != nil {
		writeJSONError(w, err.Error(), http.StatusInternalServerError)
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
		http.Error(w, `{"error":"project store not initialized"}`, http.StatusServiceUnavailable)
		return
	}
	var p config.Project
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	p.ID = projectID
	if err := s.projectStore.Update(p); err != nil {
		writeJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, s.projectStore.Get(projectID))
}

// handleProjectDelete handles DELETE /api/projects/{pid}.
func (s *Server) handleProjectDelete(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("pid")
	if s.projectStore == nil {
		http.Error(w, `{"error":"project store not initialized"}`, http.StatusServiceUnavailable)
		return
	}
	if err := s.projectStore.Remove(projectID); err != nil {
		writeJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
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
			Nodelet: config.NodeletConfig{ID: nid, Name: nid},
		}
		if ok {
			sw.Nodelet = item
		} else {
			sw.Error = "nodelet not found in config"
		}
		results[i] = sw
	}

	writeJSON(w, results)
}

func (s *Server) addProjectServer(w http.ResponseWriter, r *http.Request, projectID string) {
	if s.projectStore == nil {
		http.Error(w, `{"error":"project store not initialized"}`, http.StatusServiceUnavailable)
		return
	}

	var req struct {
		NodeletID string `json:"nodeletId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.NodeletID == "" {
		http.Error(w, `{"error":"nodeletId is required"}`, http.StatusBadRequest)
		return
	}

	// 验证 nodelet 存在。
	if _, ok := s.findNodelet(req.NodeletID); !ok {
		http.Error(w, `{"error":"nodelet not found in config"}`, http.StatusBadRequest)
		return
	}

	if err := s.projectStore.AddNodelet(projectID, req.NodeletID); err != nil {
		writeJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
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
			State:       c.State,
			Health:      c.Health,
			ServiceType: string(stype),
		}
	}

	writeJSON(w, result)
}
