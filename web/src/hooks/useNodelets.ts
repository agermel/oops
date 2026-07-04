import { useQuery } from "@tanstack/react-query";
import { apiRequest } from "../lib/api";
import { nodeletBasePaths } from "../lib/paths";
import { queryKeys } from "./queries";
import type { NodeletConfig } from "../types";

export function useNodelets() {
  return useQuery<NodeletConfig[]>({
    queryKey: queryKeys.nodelets.all,
    queryFn: () => apiRequest<NodeletConfig[]>(nodeletBasePaths.list),
    staleTime: 60_000,
  });
}
