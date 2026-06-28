import { useState, useEffect, useMemo, useCallback } from "react";

// ---- Route 类型 ----

export type Route =
  | { view: "projects" }
  | { view: "console" }
  | { view: "project-overview"; projectId: string; serverId?: string; containerId?: string }
  | { view: "project-mcp"; projectId: string }
  | { view: "project-chat"; projectId: string }
  | { view: "project-console"; projectId: string };

// ---- 解析 & 序列化 ----

export function parseHashRoute(hash: string): Route {
  const path = hash.replace(/^#\/?/, "");
  const segs = path.split("/").filter(Boolean);

  // #/console
  if (segs[0] === "console") {
    return { view: "console" };
  }

  // 根路径 或 #/projects
  if (segs.length === 0 || (segs[0] === "projects" && segs.length === 1)) {
    return { view: "projects" };
  }

  // 必须以 projects 开头且至少有 projectId
  if (segs[0] !== "projects" || segs.length < 2) {
    return { view: "projects" };
  }

  const projectId = decodeURIComponent(segs[1]);

  // #/projects/:pid
  if (segs.length === 2) {
    return { view: "project-overview", projectId };
  }

  // #/projects/:pid/mcp
  if (segs[2] === "mcp") {
    return { view: "project-mcp", projectId };
  }

  // #/projects/:pid/chat
  if (segs[2] === "chat") {
    return { view: "project-chat", projectId };
  }

  // #/projects/:pid/console
  if (segs[2] === "console") {
    return { view: "project-console", projectId };
  }

  // #/projects/:pid/servers/:nid[/containers/:cid]
  if (segs[2] === "servers" && segs.length >= 4) {
    const serverId = segs[3];
    if (segs.length >= 6 && segs[4] === "containers") {
      return { view: "project-overview", projectId, serverId, containerId: segs[5] };
    }
    return { view: "project-overview", projectId, serverId };
  }

  // 其他 /projects/:pid/... 回退到概览
  return { view: "project-overview", projectId };
}

export function routeToHash(route: Route): string {
  switch (route.view) {
    case "projects":
      return "#/projects";
    case "console":
      return "#/console";
    case "project-overview": {
      let h = `#/projects/${encodeURIComponent(route.projectId)}`;
      if (route.serverId) {
        h += `/servers/${route.serverId}`;
        if (route.containerId) h += `/containers/${route.containerId}`;
      }
      return h;
    }
    case "project-mcp":
      return `#/projects/${encodeURIComponent(route.projectId)}/mcp`;
    case "project-chat":
      return `#/projects/${encodeURIComponent(route.projectId)}/chat`;
    case "project-console":
      return `#/projects/${encodeURIComponent(route.projectId)}/console`;
  }
}

// ---- Hook ----

export function useHashRouter() {
  const [hash, setHash] = useState<string>(() => window.location.hash);

  useEffect(() => {
    const onHashChange = () => setHash(window.location.hash);
    window.addEventListener("hashchange", onHashChange);
    // 首次挂载时同步一次（处理 StrictMode 或 race）
    setHash(window.location.hash);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, []);

  const route = useMemo(() => parseHashRoute(hash), [hash]);

  const navigate = useCallback((to: Route) => {
    window.location.hash = routeToHash(to);
  }, []);

  const replace = useCallback((to: Route) => {
    const newHash = routeToHash(to);
    window.history.replaceState(null, "", `#${newHash.replace(/^#/, "")}`);
    setHash(newHash);
  }, []);

  return { route, navigate, replace };
}
