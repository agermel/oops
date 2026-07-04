import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiRequest } from "../lib/api";
import { projectPaths, serverPaths, nodeletPaths, nodeletBasePaths, mcpConnectionPaths } from "../lib/paths";
import { queryKeys } from "./queries";
import type { ServerWithNodelet, NodeletStatusItem, ContainerWithType, MCPConnectionStatus, ProjectMCPConnection } from "../types";

// ---- Servers ----

export function useProjectServers(projectId: string) {
  return useQuery<ServerWithNodelet[]>({
    queryKey: queryKeys.servers.byProject(projectId),
    queryFn: async () => {
      const { servers: url } = projectPaths(projectId);
      return apiRequest<ServerWithNodelet[]>(url);
    },
    enabled: !!projectId,
  });
}

export function useDeleteServer(projectId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (nodeletId: string) =>
      apiRequest(`${projectPaths(projectId).servers}/${encodeURIComponent(nodeletId)}`, {
        method: "DELETE",
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.servers.byProject(projectId) });
      qc.invalidateQueries({ queryKey: queryKeys.projects.all });
    },
  });
}

export function useAddServer(projectId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (nodeletId: string) =>
      apiRequest(projectPaths(projectId).servers, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ nodeletId }),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.servers.byProject(projectId) });
      qc.invalidateQueries({ queryKey: queryKeys.projects.all });
    },
  });
}

// ---- Prober Status (polling) ----

export function useNodeletStatus() {
  return useQuery<NodeletStatusItem[]>({
    queryKey: queryKeys.nodelets.status,
    queryFn: () => apiRequest<NodeletStatusItem[]>(nodeletBasePaths.status),
    staleTime: 30_000,
    refetchInterval: 30_000, // 30s 轮询
  });
}

// ---- Containers ----

export function useContainers(projectId: string, nodeletId: string) {
  return useQuery<ContainerWithType[]>({
    queryKey: queryKeys.containers.byServer(projectId, nodeletId),
    queryFn: () => apiRequest<ContainerWithType[]>(serverPaths(projectId, nodeletId).containers),
    enabled: !!projectId && !!nodeletId,
  });
}

// ---- Container Exclusions ----

export function useExcludeContainer(projectId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ nodeletId, containerId }: { nodeletId: string; containerId: string }) =>
      apiRequest(projectPaths(projectId).excludedContainers, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ nodeletId, containerId }),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.projects.all });
    },
  });
}

export function useIncludeContainer(projectId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ nodeletId, containerId }: { nodeletId: string; containerId: string }) => {
      const params = new URLSearchParams({ nodeletId, containerId });
      return apiRequest(`${projectPaths(projectId).excludedContainers}?${params}`, {
        method: "DELETE",
      });
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.projects.all });
    },
  });
}

// ---- MCP Connections ----

export function useMCPConnections() {
  return useQuery<MCPConnectionStatus[]>({
    queryKey: queryKeys.mcp.all,
    queryFn: () => apiRequest<MCPConnectionStatus[]>(mcpConnectionPaths.list),
    refetchInterval: 30_000,
  });
}

export function useProjectMCPConnections(projectId: string) {
  return useQuery<ProjectMCPConnection[]>({
    queryKey: queryKeys.mcp.byProject(projectId),
    queryFn: () => apiRequest<ProjectMCPConnection[]>(projectPaths(projectId).mcpConnections),
    enabled: !!projectId,
    refetchInterval: 30_000,
  });
}
