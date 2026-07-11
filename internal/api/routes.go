package api

import "net/http"

// Routes 返回中心端 API 路由。
func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	s.Mount(mux)
	return mux
}

// Mount 把中心端 API 路由挂载到指定 mux。
// Go 1.22+ 原生支持方法和路径参数匹配，不再需要手工 TrimPrefix+Split 解析。
func (s *Server) Mount(mux *http.ServeMux) {
	// 所有 API 路由统一经过: securityHeaders → rateLimit → authMiddleware → requireAuth → limitBody → handler
	authed := func(f http.HandlerFunc) http.HandlerFunc {
		return securityHeaders(s.trackRequest(s.rateLimit(s.authMiddleware(s.requireAuth(limitBody(f))))))
	}
	authedRun := func(f http.HandlerFunc) http.HandlerFunc {
		return securityHeaders(s.trackRequest(s.rateLimitRun(s.authMiddleware(s.requireAuth(limitBody(f))))))
	}
	publicWrap := func(f http.HandlerFunc) http.HandlerFunc {
		return securityHeaders(s.trackRequest(s.rateLimit(s.authMiddleware(f))))
	}

	// ---- Auth ----
	mux.HandleFunc("POST /api/token", publicWrap(s.handleCreateToken))
	mux.HandleFunc("DELETE /api/token", publicWrap(s.handleDeleteToken))
	mux.HandleFunc("GET /api/auth/me", authed(s.handleAuthMe))
	mux.HandleFunc("GET /api/auth/status", publicWrap(s.handleAuthStatus))
	mux.HandleFunc("POST /api/setup", publicWrap(s.handleSetup))

	// ---- Nodelets ----
	mux.HandleFunc("GET /api/nodelets", authed(s.handleNodeletList))
	mux.HandleFunc("GET /api/nodelets/status", authed(s.handleNodelets))
	mux.HandleFunc("POST /api/nodelets", authed(s.handleNodeletAdd))
	mux.HandleFunc("PUT /api/nodelets/{id}", authed(s.handleNodeletUpdate))
	mux.HandleFunc("DELETE /api/nodelets/{id}", authed(s.handleNodeletRemove))
	mux.HandleFunc("POST /api/nodelets/test", authed(s.handleNodeletTest))
	mux.HandleFunc("POST /api/nodelets/{id}/probe", authed(s.handleNodeletProbe))
	mux.HandleFunc("POST /api/nodelets/probe-all", authed(s.handleNodeletProbeAll))
	mux.HandleFunc("GET /api/nodelets/{nodeletID}/containers", authed(s.handleNodeletContainers))
	mux.HandleFunc("GET /api/nodelets/{nodeletID}/containers/{containerID}/logs", authed(s.handleNodeletLogs))
	mux.HandleFunc("GET /api/nodelets/{nodeletID}/containers/{containerID}/logs/stream", authed(s.handleNodeletLogsStreamRoute))

	// ---- Chat & Sessions ----
	mux.HandleFunc("POST /api/runs", authedRun(s.handleRunCreate))
	mux.HandleFunc("GET /api/runs/{id}/events", authed(s.handleRunEvents))
	mux.HandleFunc("POST /api/runs/{id}/abort", authed(s.handleRunAbort))
	mux.HandleFunc("GET /api/agent-settings", authed(s.handleAgentSettingsGet))
	mux.HandleFunc("PUT /api/agent-settings", authed(s.handleAgentSettingsUpdate))
	mux.HandleFunc("GET /api/sessions", authed(s.handleSessions))
	mux.HandleFunc("GET /api/sessions/{id}", authed(s.handleSessionGet))
	mux.HandleFunc("PATCH /api/sessions/{id}", authed(s.handleSessionUpdate))
	mux.HandleFunc("DELETE /api/sessions/{id}", authed(s.handleSessionDelete))
	mux.HandleFunc("POST /api/sessions/{id}/branch", authed(s.handleSessionBranch))

	// ---- MCP Connections ----
	mux.HandleFunc("GET /api/mcp/connections", authed(s.handleMCPList))
	mux.HandleFunc("POST /api/mcp/connections", authed(s.handleMCPAdd))
	mux.HandleFunc("PUT /api/mcp/connections/{id}", authed(s.handleMCPUpdate))
	mux.HandleFunc("DELETE /api/mcp/connections/{id}", authed(s.handleMCPRemove))
	mux.HandleFunc("GET /api/mcp/connections/{id}/logs", authed(s.handleMCPLogs))
	mux.HandleFunc("GET /api/mcp/connections/{id}/logs/stream", authed(s.handleMCPLogsStream))
	mux.HandleFunc("POST /api/mcp/connections/{id}/tools/{toolName}/test", authed(s.handleMCPToolTestRoute))
	mux.HandleFunc("POST /api/mcp/connections/test", authed(s.handleMCPTest))

	// ---- Tools ----
	mux.HandleFunc("GET /api/tools", authed(s.handleTools))
	mux.HandleFunc("PUT /api/tools/{name}", authed(s.handleToolToggle))

	// ---- Skills ----
	mux.HandleFunc("GET /api/skills", authed(s.handleSkillsList))
	mux.HandleFunc("PUT /api/skills/{name}", authed(s.handleSkillsUpdate))
	mux.HandleFunc("DELETE /api/skills/{name}", authed(s.handleSkillsDelete))

	// ---- Console SSE ----
	mux.HandleFunc("GET /api/console/stream", securityHeaders(s.trackRequest(s.authMiddleware(s.handleConsoleStream))))

	// ---- Projects ----
	mux.HandleFunc("GET /api/projects", authed(s.handleProjectList))
	mux.HandleFunc("POST /api/projects", authed(s.handleProjectCreate))
	mux.HandleFunc("GET /api/projects/{pid}", authed(s.handleProjectGet))
	mux.HandleFunc("PUT /api/projects/{pid}", authed(s.handleProjectUpdate))
	mux.HandleFunc("DELETE /api/projects/{pid}", authed(s.handleProjectDelete))

	// ---- Project Container Exclusions ----
	mux.HandleFunc("POST /api/projects/{pid}/excluded-containers", authed(s.handleProjectExcludeContainer))
	mux.HandleFunc("DELETE /api/projects/{pid}/excluded-containers", authed(s.handleProjectIncludeContainer))

	// ---- Project Sessions ----
	mux.HandleFunc("GET /api/projects/{pid}/sessions", authed(s.handleProjectSessions))
	mux.HandleFunc("GET /api/projects/{pid}/sessions/{id}", authed(s.handleProjectSessionGet))
	mux.HandleFunc("PATCH /api/projects/{pid}/sessions/{id}", authed(s.handleProjectSessionUpdate))
	mux.HandleFunc("DELETE /api/projects/{pid}/sessions/{id}", authed(s.handleProjectSessionDelete))

	// ---- Project Servers ----
	mux.HandleFunc("GET /api/projects/{pid}/servers", authed(s.handleProjectServersList))
	mux.HandleFunc("POST /api/projects/{pid}/servers", authed(s.handleProjectServersAdd))
	mux.HandleFunc("DELETE /api/projects/{pid}/servers/{sid}", authed(s.handleProjectServersRemove))

	// ---- Project MCP Connections ----
	mux.HandleFunc("GET /api/projects/{pid}/mcp/connections", authed(s.handleProjectMCPList))

	// ---- Project Containers ----
	mux.HandleFunc("GET /api/projects/{pid}/servers/{sid}/containers", authed(s.handleProjectContainers))
	mux.HandleFunc("GET /api/projects/{pid}/servers/{sid}/containers/{cid}", authed(s.handleContainerDetail))
	mux.HandleFunc("GET /api/projects/{pid}/servers/{sid}/containers/{cid}/logs/stream", authed(s.handleProjectLogsStream))
	mux.HandleFunc("GET /api/projects/{pid}/servers/{sid}/containers/{cid}/mcp", authed(s.handleContainerMCPGet))
	mux.HandleFunc("DELETE /api/projects/{pid}/servers/{sid}/containers/{cid}/mcp", authed(s.handleContainerMCPDelete))
	mux.HandleFunc("GET /api/projects/{pid}/servers/{sid}/containers/{cid}/dsn", authed(s.handleContainerDSNGet))
	mux.HandleFunc("PUT /api/projects/{pid}/servers/{sid}/containers/{cid}/dsn", authed(s.handleContainerDSNPut))
	mux.HandleFunc("DELETE /api/projects/{pid}/servers/{sid}/containers/{cid}/dsn", authed(s.handleContainerDSNDelete))
}

func (s *Server) trackRequest(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		requestCtx, finishRequest, ok := s.beginRequest(r.Context())
		if !ok {
			writeJSONError(w, "server is shutting down", http.StatusServiceUnavailable)
			return
		}
		defer finishRequest()
		next(w, r.WithContext(requestCtx))
	}
}

func (s *Server) handleConsoleStream(w http.ResponseWriter, r *http.Request) {
	streamCtx, finishStream, ok := s.beginStream(r.Context())
	if !ok {
		writeJSONError(w, "server is shutting down", http.StatusServiceUnavailable)
		return
	}
	defer finishStream()
	s.consoleHub.SSEHandler(w, r.WithContext(streamCtx))
}
