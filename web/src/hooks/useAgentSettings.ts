import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiRequest } from "../lib/api";
import { agentSettingsPaths } from "../lib/paths";
import type { AgentSettings } from "../types";
import { queryKeys } from "./queries";

export function useAgentSettings() {
  return useQuery<AgentSettings>({
    queryKey: queryKeys.agentSettings.detail,
    queryFn: () => apiRequest<AgentSettings>(agentSettingsPaths.detail),
  });
}

export function useUpdateAgentSettings() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (settings: AgentSettings) =>
      apiRequest<AgentSettings>(agentSettingsPaths.detail, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(settings),
      }),
    onSuccess: (settings) => {
      queryClient.setQueryData(queryKeys.agentSettings.detail, settings);
      queryClient.invalidateQueries({ queryKey: queryKeys.agentSettings.detail });
    },
  });
}
