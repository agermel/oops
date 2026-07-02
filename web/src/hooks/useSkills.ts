import { useQuery } from "@tanstack/react-query";
import { apiRequest } from "../lib/api";
import { queryKeys } from "./queries";
import type { Skill } from "../types";

export function useSkills() {
  return useQuery<Skill[]>({
    queryKey: queryKeys.skills.all,
    queryFn: async () => {
      const list = await apiRequest<Skill[]>("/api/skills");
      return list || [];
    },
  });
}
