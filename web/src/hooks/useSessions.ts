import { useQuery } from "@tanstack/react-query";
import { apiRequest } from "../lib/api";
import { projectPaths, sessionPaths } from "../lib/paths";
import { queryKeys } from "./queries";
import type { SessionInfo, SessionResponse } from "../types";

export function useSessions(projectId?: string) {
  return useQuery<SessionInfo[]>({
    queryKey: queryKeys.sessions.all(projectId),
    queryFn: async () => {
      if (!projectId) return [];
      const list = await apiRequest<SessionInfo[]>(projectPaths(projectId).sessions);
      return list || [];
    },
    enabled: !!projectId,
  });
}

export function useSessionDetail(sessionId: string) {
  return useQuery<SessionResponse>({
    queryKey: queryKeys.sessions.detail(sessionId),
    queryFn: () =>
      apiRequest<SessionResponse>(sessionPaths(sessionId).get + "?include_messages=true"),
    enabled: !!sessionId,
  });
}
