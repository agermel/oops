import { useState, useEffect, useMemo, useCallback } from "react";

// ---- Route 类型（与 useHashRouter 完全一致）----

export type Route =
  | { view: "projects" }
  | { view: "servers" }
  | { view: "console" }
  | { view: "project-overview"; projectId: string }
  | { view: "project-mcp"; projectId: string }
  | { view: "project-chat"; projectId: string }
  | { view: "project-console"; projectId: string }
  | { view: "project-tools"; projectId: string }
  | { view: "project-skills"; projectId: string };

// ---- 解析 & 序列化 ----

export function parsePathRoute(pathname: string): Route {
  // 去掉可能的 base prefix（如 /oops），然后按 / 拆分
  const segs = pathname.split("/").filter(Boolean);

  // /servers
  if (segs[0] === "servers") {
    return { view: "servers" };
  }

  // /console
  if (segs[0] === "console") {
    return { view: "console" };
  }

  // 根路径
  if (segs.length === 0) {
    return { view: "projects" };
  }

  // 必须以 projects 开头且至少有 projectId
  if (segs[0] !== "projects" || segs.length < 2) {
    return { view: "projects" };
  }

  let projectId: string;
  try {
    projectId = decodeURIComponent(segs[1]);
  } catch {
    // malformed percent-encoding → fall back to project list
    return { view: "projects" };
  }
  if (!projectId) return { view: "projects" };

  // /projects/:pid
  if (segs.length === 2) {
    return { view: "project-overview", projectId };
  }

  // /projects/:pid/mcp
  if (segs[2] === "mcp") {
    return { view: "project-mcp", projectId };
  }

  // /projects/:pid/chat
  if (segs[2] === "chat") {
    return { view: "project-chat", projectId };
  }

  // /projects/:pid/console
  if (segs[2] === "console") {
    return { view: "project-console", projectId };
  }

  // /projects/:pid/tools
  if (segs[2] === "tools") {
    return { view: "project-tools", projectId };
  }

  // /projects/:pid/skills
  if (segs[2] === "skills") {
    return { view: "project-skills", projectId };
  }

  // 其他 /projects/:pid/... 回退到概览
  return { view: "project-overview", projectId };
}

export function routeToPath(route: Route): string {
  switch (route.view) {
    case "projects":
      return "/";
    case "servers":
      return "/servers";
    case "console":
      return "/console";
    case "project-overview":
      return `/projects/${encodeURIComponent(route.projectId)}`;
    case "project-mcp":
      return `/projects/${encodeURIComponent(route.projectId)}/mcp`;
    case "project-chat":
      return `/projects/${encodeURIComponent(route.projectId)}/chat`;
    case "project-console":
      return `/projects/${encodeURIComponent(route.projectId)}/console`;
    case "project-tools":
      return `/projects/${encodeURIComponent(route.projectId)}/tools`;
    case "project-skills":
      return `/projects/${encodeURIComponent(route.projectId)}/skills`;
  }
}

// ---- Hook ----

export function usePathRouter() {
  const [pathname, setPathname] = useState<string>(() => window.location.pathname);

  useEffect(() => {
    const onPopState = () => setPathname(window.location.pathname);
    window.addEventListener("popstate", onPopState);
    // 首次挂载时同步一次（处理 StrictMode 或 race）。
    setPathname(window.location.pathname);
    return () => window.removeEventListener("popstate", onPopState);
  }, []);

  const route = useMemo(() => parsePathRoute(pathname), [pathname]);

  const navigate = useCallback((to: Route) => {
    const path = routeToPath(to);
    window.history.pushState(null, "", path);
    setPathname(path);
  }, []);

  const replace = useCallback((to: Route) => {
    const path = routeToPath(to);
    window.history.replaceState(null, "", path);
    setPathname(path);
  }, []);

  return { route, navigate, replace };
}
