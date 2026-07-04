import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiRequest, getErrorMessage } from "../lib/api";
import { queryKeys } from "./queries";
import { projectBasePaths, projectPaths } from "../lib/paths";
import type { Project } from "../types";

export function useProjects() {
  return useQuery<Project[]>({
    queryKey: queryKeys.projects.all,
    queryFn: () => apiRequest<Project[]>(projectBasePaths.list),
  });
}

export function useCreateProject() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { name: string; description?: string; githubRepo?: string }) =>
      apiRequest<Project>(projectBasePaths.list, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.projects.all });
    },
  });
}

export function useDeleteProject() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiRequest(projectPaths(id).detail, { method: "DELETE" }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.projects.all });
    },
  });
}

export function useUpdateProject() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: { id: string; name?: string; description?: string; githubRepo?: string }) =>
      apiRequest<Project>(projectPaths(id).detail, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      }),
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: queryKeys.projects.detail(vars.id) });
      qc.invalidateQueries({ queryKey: queryKeys.projects.all });
    },
  });
}

export { getErrorMessage };
