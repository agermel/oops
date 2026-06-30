export type AutoExpandInput = {
  authenticated: boolean;
  view: string;
  serverCount: number;
  serversLoading: boolean;
  expandedCount: number;
  autoExpandConsumed: boolean;
};

export function shouldAutoExpandFirstServer(input: AutoExpandInput): boolean {
  if (!input.authenticated || input.view !== "project-overview") return false;
  if (input.serverCount === 0 || input.serversLoading) return false;
  if (input.expandedCount > 0) return false;
  if (input.autoExpandConsumed) return false;
  return true;
}
