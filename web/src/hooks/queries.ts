export const queryKeys = {
  projects: {
    all: ["projects"] as const,
    detail: (id: string) => ["projects", id] as const,
  },
  servers: {
    byProject: (projectId: string) => ["servers", projectId] as const,
  },
  containers: {
    byServer: (projectId: string, nodeletId: string) =>
      ["containers", projectId, nodeletId] as const,
  },
  containerDetail: {
    byId: (projectId: string, nodeletId: string, containerId: string) =>
      ["containerDetail", projectId, nodeletId, containerId] as const,
  },
  sessions: {
    all: (projectId?: string) =>
      projectId ? ["sessions", projectId] as const : ["sessions"] as const,
    detail: (sessionId: string) => ["sessions", "detail", sessionId] as const,
  },
  skills: {
    all: ["skills"] as const,
  },
  nodelets: {
    all: ["nodelets"] as const,
    status: ["nodelets", "status"] as const,
  },
};
