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

export type ContainerStatusInput = {
  containerState: string;
  stale: boolean;
};

export type ContainerStatusPresentation = {
  alive: boolean;
  unknown: boolean;
  disabled: boolean;
  title: string;
};

export function containerStatusPresentation(input: ContainerStatusInput): ContainerStatusPresentation {
  if (input.stale) {
    return {
      alive: false,
      unknown: true,
      disabled: true,
      title: "状态未知",
    };
  }

  const alive = input.containerState === "running";
  return {
    alive,
    unknown: false,
    disabled: false,
    title: alive ? "运行中" : "已停止",
  };
}
