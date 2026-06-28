package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"oops/internal/config"
	"oops/internal/docker"
)

// serverWithNodelet 是项目详情中一台服务器的聚合视图。
type serverWithNodelet struct {
	Nodelet  config.NodeletConfig `json:"nodelet"`
	Host     nodeletHostSummary   `json:"host"`
	Error    string               `json:"error,omitempty"`
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

// handleProjects 处理 /api/projects 路由（GET/POST）。
func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if s.projectStore == nil {
			writeJSON(w, []config.Project{})
			return
		}
		writeJSON(w, s.projectStore.List())

	case http.MethodPost:
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

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// handleProject 处理 /api/projects/:id 路由（GET/PUT/DELETE）。
func (s *Server) handleProject(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimPrefix(r.URL.Path, "/api/projects/")
	if projectID == "" || strings.Contains(projectID, "/") {
		writeJSONError(w, "not found", http.StatusNotFound)
		return
	}

	switch r.Method {
	case http.MethodGet:
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

	case http.MethodPut:
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

	case http.MethodDelete:
		if s.projectStore == nil {
			http.Error(w, `{"error":"project store not initialized"}`, http.StatusServiceUnavailable)
			return
		}
		if err := s.projectStore.Remove(projectID); err != nil {
			writeJSONError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// handleProjectServers 处理 /api/projects/:pid/servers 路由。
// GET: 返回项目下所有服务器的状态列表。
// POST: 向项目添加一台服务器。
func (s *Server) handleProjectServers(w http.ResponseWriter, r *http.Request) {
	projectID, _, ok := splitProjectServersPath(r.URL.Path)
	if !ok {
		writeJSONError(w, "not found", http.StatusNotFound)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.listProjectServers(w, r, projectID)

	case http.MethodPost:
		s.addProjectServer(w, r, projectID)

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
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

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	results := make([]serverWithNodelet, len(p.NodeletIDs))
	for i, nid := range p.NodeletIDs {
		item, ok := s.findNodelet(nid)
		sw := serverWithNodelet{
			Nodelet: config.NodeletConfig{ID: nid, Name: nid},
		}
		if ok {
			sw.Nodelet = item
			host, err := s.nodeletClient.Host(ctx, item.Address, item.Token)
			if err != nil {
				sw.Error = err.Error()
			} else {
				sw.Host = nodeletHostSummary{
					Available:     true,
					DockerVersion: host.DockerVersion,
					Runtime:       host.Runtime,
					NCPU:          host.NCPU,
					MemTotal:      host.MemTotal,
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

// handleProjectContainers 处理 /api/projects/:pid/servers/:sid/containers 路由。
// GET: 返回服务器容器列表（含服务类型识别）。
func (s *Server) handleProjectContainers(w http.ResponseWriter, r *http.Request) {
	_, nodeletID, containerID, _ := splitContainerPath(r.URL.Path)

	// 如果路径中有容器 ID，返回容器详情。
	if containerID != "" {
		s.handleContainerDetail(w, r)
		return
	}

	// 否则返回容器列表。
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

// splitProjectServersPath 解析 /api/projects/:pid/servers 路径。
func splitProjectServersPath(rawPath string) (string, string, bool) {
	rest := strings.TrimPrefix(rawPath, "/api/projects/")
	parts := strings.Split(rest, "/")
	if len(parts) >= 2 && parts[1] == "servers" {
		return parts[0], "", true
	}
	return "", "", false
}

// splitContainerPath 解析路径中的 nodeletID 和 containerID。
// 支持:
//
//	/api/projects/:pid/servers/:sid/containers
//	/api/projects/:pid/servers/:sid/containers/:cid
//	/api/projects/:pid/servers/:sid/containers/:cid/...
func splitContainerPath(rawPath string) (string, string, string, bool) {
	rest := strings.TrimPrefix(rawPath, "/api/projects/")
	parts := strings.Split(rest, "/")
	// parts[0]=pid, parts[1]=servers, parts[2]=sid, parts[3]=containers, parts[4]=cid?
	if len(parts) < 4 || parts[1] != "servers" || parts[3] != "containers" {
		return "", "", "", false
	}
	nodeletID := parts[2]
	containerID := ""
	if len(parts) >= 5 {
		containerID = parts[4]
	}
	return "", nodeletID, containerID, true
}
