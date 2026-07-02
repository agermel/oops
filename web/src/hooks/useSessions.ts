import { useQuery } from "@tanstack/react-query";
import { apiRequest } from "../lib/api";
import { sessionPaths } from "../lib/paths";
import { queryKeys } from "./queries";
import type { SessionInfo, SessionDetail } from "../types";

export function useSessions(projectId?: string) {
  return useQuery<SessionInfo[]>({
    queryKey: queryKeys.sessions.all(projectId),
    queryFn: async () => {
      const qs = projectId ? `?project_id=${encodeURIComponent(projectId)}` : "";
      const list = await apiRequest<SessionInfo[]>(`/api/sessions${qs}`);
      return list || [];
    },
    enabled: projectId !== undefined,
  });
}

export function useSessionDetail(sessionId: string) {
  return useQuery<SessionDetail>({
    queryKey: queryKeys.sessions.detail(sessionId),
    queryFn: () =>
      apiRequest<SessionDetail>(sessionPaths(sessionId).get + "?include_messages=true"),
    enabled: !!sessionId,
  });
}
