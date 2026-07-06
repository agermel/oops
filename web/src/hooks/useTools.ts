import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiRequest } from "../lib/api";
import { toolPaths } from "../lib/paths";
import { queryKeys } from "./queries";

export type ToolItem = {
  name: string;
  description: string;
  enabled: boolean;
  originalName?: string;
  modelName?: string;
  connectionType?: string;
};

export type ToolsData = {
  native: ToolItem[];
  mcp: Record<string, ToolItem[]>;
};

export function useTools() {
  return useQuery<ToolsData>({
    queryKey: queryKeys.tools.all,
    queryFn: () => apiRequest<ToolsData>(toolPaths.list),
  });
}

export function useToolToggle() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({ name, enabled }: { name: string; enabled: boolean }) =>
      apiRequest(toolPaths.detail(name), {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ enabled }),
      }),
    onMutate: async ({ name, enabled }) => {
      await queryClient.cancelQueries({ queryKey: queryKeys.tools.all });
      const previous = queryClient.getQueryData<ToolsData>(queryKeys.tools.all);
      queryClient.setQueryData<ToolsData>(queryKeys.tools.all, (prev) => {
        if (!prev) return prev;
        const update = (items: ToolItem[]) =>
          items.map((t) => (t.name === name ? { ...t, enabled } : t));
        return {
          native: update(prev.native),
          mcp: Object.fromEntries(
            Object.entries(prev.mcp).map(([conn, tools]) => [conn, update(tools)]),
          ),
        };
      });
      return { previous };
    },
    onError: (_err, _vars, context) => {
      if (context?.previous) {
        queryClient.setQueryData(queryKeys.tools.all, context.previous);
      }
    },
    onSettled: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.tools.all });
    },
  });
}
