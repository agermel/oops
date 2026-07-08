import { useQuery } from "@tanstack/react-query";
import { apiRequest } from "../lib/api";
import { sessionPaths, sessionBasePaths } from "../lib/paths";
import { queryKeys } from "./queries";
import type { SessionInfo, SessionResponse } from "../types";

export function useSessions(projectId?: string) {
  return useQuery<SessionInfo[]>({
    queryKey: queryKeys.sessions.all(projectId),
    queryFn: async () => {
      const url = projectId
        ? `${sessionBasePaths.list}?project_id=${encodeURIComponent(projectId)}`
        : sessionBasePaths.list;
      const list = await apiRequest<SessionInfo[]>(url);
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
