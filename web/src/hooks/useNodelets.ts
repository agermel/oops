import { useQuery } from "@tanstack/react-query";
import { apiRequest } from "../lib/api";
import { queryKeys } from "./queries";
import type { NodeletConfig } from "../types";

export function useNodelets() {
  return useQuery<NodeletConfig[]>({
    queryKey: queryKeys.nodelets.all,
    queryFn: () => apiRequest<NodeletConfig[]>("/api/nodelets"),
    staleTime: 60_000,
  });
}
