function enc(v: string): string {
  return encodeURIComponent(v);
}

export function projectPaths(projectId: string) {
  const pid = enc(projectId);
  return {
    servers: `/api/projects/${pid}/servers`,
    chat: `/api/projects/${pid}/chat`,
    sessions: `/api/projects/${pid}/sessions`,
    session: (sid: string) => `/api/projects/${pid}/sessions/${enc(sid)}`,
    excludedContainers: `/api/projects/${pid}/excluded-containers`,
  };
}

export function sessionPaths(sessionId: string) {
  const sid = enc(sessionId);
  return {
    get: `/api/sessions/${sid}`,
    delete: `/api/sessions/${sid}`,
    list: "/api/sessions",
  };
}

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
