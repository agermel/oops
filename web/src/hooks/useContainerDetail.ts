import { useQuery, useMutation } from "@tanstack/react-query";
import { apiRequest } from "../lib/api";
import { serverPaths } from "../lib/paths";
import { queryKeys } from "./queries";
import type { ContainerDetail, HealthResult } from "../types";

export function useContainerDetail(
  projectId: string,
  nodeletId: string,
  containerId: string,
) {
  return useQuery<ContainerDetail>({
    queryKey: queryKeys.containerDetail.byId(projectId, nodeletId, containerId),
    queryFn: () =>
      apiRequest<ContainerDetail>(
        serverPaths(projectId, nodeletId).container(containerId),
      ),
    enabled: !!projectId && !!nodeletId && !!containerId,
  });
}

export function useCheckHealth(
  projectId: string,
  nodeletId: string,
  containerId: string,
) {
  return useMutation({
    mutationFn: () =>
      apiRequest<HealthResult>(
        serverPaths(projectId, nodeletId).containerCheck(containerId),
        { method: "POST" },
      ),
  });
}
