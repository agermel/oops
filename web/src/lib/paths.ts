function enc(v: string): string {
  return encodeURIComponent(v);
}

// ---- Projects ----

export function projectPaths(projectId: string) {
  const pid = enc(projectId);
  return {
    detail: `/api/projects/${pid}`,
    servers: `/api/projects/${pid}/servers`,
    sessions: `/api/projects/${pid}/sessions`,
    session: (sid: string) => `/api/projects/${pid}/sessions/${enc(sid)}`,
    excludedContainers: `/api/projects/${pid}/excluded-containers`,
    mcpConnections: `/api/projects/${pid}/mcp/connections`,
  };
}

export const projectBasePaths = {
  list: "/api/projects",
  create: "/api/projects",
} as const;

// ---- Sessions ----

export function sessionPaths(sessionId: string) {
  const sid = enc(sessionId);
  return {
    get: `/api/sessions/${sid}`,
    update: `/api/sessions/${sid}`,
    delete: `/api/sessions/${sid}`,
    branch: `/api/sessions/${sid}/branch`,
  };
}

// ---- Servers ----

export function serverPaths(projectId: string, serverId: string) {
  const pid = enc(projectId);
  const nid = enc(serverId);
  const base = `/api/projects/${pid}/servers/${nid}`;
  return {
    containers: `${base}/containers`,
    container: (cid: string) => `${base}/containers/${enc(cid)}`,
    containerLogs: (cid: string) => `${base}/containers/${enc(cid)}/logs/stream?tail=100`,
    containerMCP: (cid: string) => `${base}/containers/${enc(cid)}/mcp`,
    containerDSN: (cid: string) => `${base}/containers/${enc(cid)}/dsn`,
  };
}

// ---- Nodelets ----

export function nodeletPaths(id: string) {
  return {
    list: "/api/nodelets",
    detail: `/api/nodelets/${enc(id)}`,
    probe: `/api/nodelets/${enc(id)}/probe`,
    test: "/api/nodelets/test",
    probeAll: "/api/nodelets/probe-all",
    status: "/api/nodelets/status",
  };
}

export const nodeletBasePaths = {
  list: "/api/nodelets",
  test: "/api/nodelets/test",
  probeAll: "/api/nodelets/probe-all",
  status: "/api/nodelets/status",
} as const;

// ---- MCP Connections ----

export const mcpConnectionPaths = {
  list: "/api/mcp/connections",
  test: "/api/mcp/connections/test",
  detail: (id: string) => `/api/mcp/connections/${enc(id)}`,
  logs: (id: string) => `/api/mcp/connections/${enc(id)}/logs?tail=200`,
  logsStream: (id: string) => `/api/mcp/connections/${enc(id)}/logs/stream?tail=200`,
  toolTest: (connectionId: string, toolName: string) =>
    `/api/mcp/connections/${enc(connectionId)}/tools/${enc(toolName)}/test`,
  readResource: (connectionId: string, uri: string) =>
    `/api/mcp/connections/${enc(connectionId)}/resources/read?uri=${encodeURIComponent(uri)}`,
} as const;

// ---- Tools ----

export const toolPaths = {
  list: "/api/tools",
  detail: (name: string) => `/api/tools/${enc(name)}`,
} as const;

// ---- Agent Settings ----

export const agentSettingsPaths = {
  detail: "/api/agent-settings",
} as const;

// ---- Skills ----

export const skillPaths = {
  list: "/api/skills",
  detail: (name: string) => `/api/skills/${enc(name)}`,
} as const;

// ---- Auth & Misc ----

export const authPaths = {
  status: "/api/auth/status",
  me: "/api/auth/me",
  token: "/api/token",
  setup: "/api/setup",
} as const;

export const consolePaths = {
  stream: "/api/console/stream",
} as const;

export const runPaths = {
  create: "/api/runs",
  events: (runId: string) => `/api/runs/${enc(runId)}/events`,
  abort: (runId: string) => `/api/runs/${enc(runId)}/abort`,
} as const;
