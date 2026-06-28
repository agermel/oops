function enc(v: string): string {
  return encodeURIComponent(v);
}

export function projectPaths(projectId: string) {
  const pid = enc(projectId);
  return {
    servers: `/api/projects/${pid}/servers`,
    chat: `/api/projects/${pid}/chat`,
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
    containerCheck: (cid: string) => `${base}/containers/${enc(cid)}/check`,
    containerMCP: (cid: string) => `${base}/containers/${enc(cid)}/mcp`,
  };
}
