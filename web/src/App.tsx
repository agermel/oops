import React from "react";
import debounce from "lodash.debounce";
import type {
  Project,
  ServerWithNodelet,
  ContainerWithType,
  ContainerDetail as ContainerDetailType,
  HealthResult,
  LogEntry,
  StepEvent,
  ChatExchange,
} from "./types";
import { MAX_LOGS, LOG_FLUSH_MS, LOG_MAX_WAIT_MS } from "./types";
import { useHashRouter } from "./hooks/useHashRouter";
import { apiRequest, getErrorMessage } from "./lib/api";
import { projectPaths, serverPaths } from "./lib/paths";
import { Header } from "./components/Header";
import { SideRail } from "./components/SideRail";
import { ProjectsView } from "./components/ProjectsView";
import { ProjectDetailView } from "./components/ProjectDetailView";
import { ChatView } from "./components/ChatView";
import { MCPView } from "./components/MCPView";
import { ToolsView } from "./components/ToolsView";
import { ConsolePanel } from "./components/ConsolePanel";
import "./styles.css";

export function App() {
  // ---- Hash 路由（唯一导航数据源） ----
  const { route, navigate, replace } = useHashRouter();

  // 从 route 派生所有导航状态
  const activeNav = route.view === "console" ? "console" : "projects";
  const selectedProjectID =
    route.view === "projects" || route.view === "console"
      ? ""
      : (route as any).projectId || "";
  const projectSection: string =
    route.view === "project-mcp" ? "mcp" :
    route.view === "project-chat" ? "chat" :
    route.view === "project-console" ? "console" :
    route.view === "project-tools" ? "tools" :
    route.view === "project-overview" ? "overview" :
    "overview";
  const urlServerId = route.view === "project-overview" ? route.serverId : undefined;
  const urlContainerId = route.view === "project-overview" ? route.containerId : undefined;

  // 供旧接口使用的派生值
  const selectedNodeletID = urlServerId || "";
  const selectedContainerID = urlContainerId || "";

  // 跟踪当前已加载的项目，避免重复加载
  const lastLoadedProjectRef = React.useRef("");

  // 侧栏折叠（纯 UI 状态）
  const [sidebarCollapsed, setSidebarCollapsed] = React.useState(false);

  // ---- 项目状态 ----
  const [projects, setProjects] = React.useState<Project[]>([]);
  const [projectsLoading, setProjectsLoading] = React.useState(false);
  const [projectsError, setProjectsError] = React.useState("");

  // ---- 服务器 & 容器树状态 ----
  const [servers, setServers] = React.useState<ServerWithNodelet[]>([]);
  const [serversLoading, setServersLoading] = React.useState(false);
  const [serverError, setServerError] = React.useState("");
  const [containers, setContainers] = React.useState<Record<string, ContainerWithType[]>>({});
  const [containersLoading, setContainersLoading] = React.useState(false);
  const [expandedServers, setExpandedServers] = React.useState<Set<string>>(new Set());

  // ---- 容器详情状态 ----
  const [containerDetail, setContainerDetail] = React.useState<ContainerDetailType | undefined>();
  const [detailLoading, setDetailLoading] = React.useState(false);
  const [detailError, setDetailError] = React.useState("");
  const [health, setHealth] = React.useState<HealthResult | undefined>();
  const [healthLoading, setHealthLoading] = React.useState(false);

  // ---- 日志状态 ----
  const [logs, setLogs] = React.useState<LogEntry[]>([]);
  const [logsLoading, setLogsLoading] = React.useState(false);
  const [logsError, setLogsError] = React.useState("");
  const [autoScroll, setAutoScroll] = React.useState(true);
  const logEventSource = React.useRef<EventSource | null>(null);
  const logBuffer = React.useRef<LogEntry[]>([]);
  const logsPanel = React.useRef<HTMLDivElement | null>(null);

  // ---- 聊天状态 ----
  const [chatExchanges, setChatExchanges] = React.useState<ChatExchange[]>([]);
  const [currentSteps, setCurrentSteps] = React.useState<StepEvent[]>([]);
  const [chatInput, setChatInput] = React.useState("");
  const [currentQuestion, setCurrentQuestion] = React.useState("");
  const [chatLoading, setChatLoading] = React.useState(false);
  const [chatError, setChatError] = React.useState("");
  const chatLoadingRef = React.useRef(false);
  const chatStepsRef = React.useRef<StepEvent[]>([]);

  // ---- 日志缓冲区 ----
  const flushLogs = React.useMemo(
    () =>
      debounce(
        () => {
          if (logBuffer.current.length === 0) return;
          const nextLogs = logBuffer.current;
          logBuffer.current = [];
          setLogs((current) => [...current, ...nextLogs].slice(-MAX_LOGS));
        },
        LOG_FLUSH_MS,
        { maxWait: LOG_MAX_WAIT_MS }
      ),
    []
  );

  function closeLogStream() {
    if (logEventSource.current) {
      logEventSource.current.close();
      logEventSource.current = null;
    }
    flushLogs.cancel();
    logBuffer.current = [];
  }

  // ---------------- 数据获取（纯函数，不依赖闭包中的导航状态） ----------------

  async function fetchProjects() {
    setProjectsLoading(true);
    setProjectsError("");
    try {
      setProjects(await apiRequest<Project[]>("/api/projects"));
    } catch (err) {
      setProjectsError(getErrorMessage(err, "读取项目列表失败"));
    } finally {
      setProjectsLoading(false);
    }
  }

  async function loadProjectServers(projectID: string) {
    setServersLoading(true);
    setServerError("");
    try {
      const { servers: url } = projectPaths(projectID);
      setServers(await apiRequest<ServerWithNodelet[]>(url));
    } catch (err) {
      setServerError(getErrorMessage(err, "读取服务器列表失败"));
    } finally {
      setServersLoading(false);
    }
  }

  async function loadContainers(projectID: string, nodeletID: string) {
    setContainersLoading(true);
    try {
      const data = await apiRequest<ContainerWithType[]>(serverPaths(projectID, nodeletID).containers);
      setContainers((prev) => ({ ...prev, [nodeletID]: data }));
    } catch (err) {
      setServerError(getErrorMessage(err, "读取容器列表失败"));
    } finally {
      setContainersLoading(false);
    }
  }

  async function selectContainer(projectId: string, nodeletID: string, containerID: string) {
    closeLogStream();
    setDetailError("");
    setHealth(undefined);

    // 加载容器详情
    setDetailLoading(true);
    try {
      setContainerDetail(await apiRequest<ContainerDetailType>(
        serverPaths(projectId, nodeletID).container(containerID)
      ));
    } catch (err) {
      setDetailError(getErrorMessage(err, "读取容器详情失败"));
      setContainerDetail(undefined);
    } finally {
      setDetailLoading(false);
    }

    // 启动日志流
    loadLogStream(projectId, nodeletID, containerID);
  }

  function loadLogStream(projectId: string, nodeletID: string, containerID: string) {
    closeLogStream();
    setLogsLoading(true);
    setLogsError("");

    const url = serverPaths(projectId, nodeletID).containerLogs(containerID);

    const source = new EventSource(url);
    logEventSource.current = source;

    source.onopen = () => {
      setLogsLoading(false);
    };
    source.onmessage = (event) => {
      try {
        logBuffer.current = [...logBuffer.current, JSON.parse(event.data) as LogEntry];
        flushLogs();
      } catch {
        // 跳过无法解析的日志行
      }
    };
    source.onerror = () => {
      setLogsLoading(false);
      setLogsError("日志流连接失败，请检查容器是否在运行");
    };
  }

  // ---- 健康检查 ----
  async function checkHealth() {
    if (!selectedContainerID || !selectedNodeletID) return;
    setHealthLoading(true);
    try {
      setHealth(await apiRequest<HealthResult>(
        serverPaths(selectedProjectID, selectedNodeletID).containerCheck(selectedContainerID),
        { method: "POST" }
      ));
    } catch (err) {
      setHealth({ status: "unknown", message: getErrorMessage(err, "探测失败"), latency: 0 });
    } finally {
      setHealthLoading(false);
    }
  }

  // ---- 聊天 ----
  async function sendChat(question?: string) {
    const q = (question ?? chatInput).trim();
    if (!q || chatLoadingRef.current) return;
    chatLoadingRef.current = true;
    setChatInput("");
    setChatError("");
    chatStepsRef.current = [];
    setCurrentSteps([]);
    setCurrentQuestion(q);
    setChatLoading(true);

    try {
      const url = selectedProjectID
        ? projectPaths(selectedProjectID).chat
        : "/api/chat";
      const resp = await fetch(url, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ question: q }),
      });
      if (!resp.ok) {
        const data = await resp.json().catch(() => ({ error: `HTTP ${resp.status}` }));
        throw new Error(data.error || `HTTP ${resp.status}`);
      }

      const reader = resp.body!.getReader();
      const decoder = new TextDecoder();
      let buffer = "";
      let streamDone = false;

      while (!streamDone) {
        const { done, value } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split("\n");
        buffer = lines.pop() || "";
        for (const line of lines) {
          if (line === "data: [DONE]") { streamDone = true; break; }
          if (line.startsWith("data: ")) {
            try {
              const evt: StepEvent = JSON.parse(line.slice(6));
              chatStepsRef.current = [...chatStepsRef.current, evt];
              setCurrentSteps(chatStepsRef.current);
            } catch { /* skip */ }
          }
        }
      }

      // 从 ref 读取最终步骤列表，避免在 setState updater 内调用另一个 setState
      const steps = chatStepsRef.current;
      const answer = steps.find((s) => s.type === "answer");
      const errStep = steps.find((s) => s.type === "error");
      setChatExchanges((prev) => [...prev, { question: q, steps, answer: answer?.content, error: errStep?.content }]);
      chatStepsRef.current = [];
      setCurrentSteps([]);
      setCurrentQuestion("");
      setChatLoading(false);
      chatLoadingRef.current = false;
    } catch (err) {
      setChatError(getErrorMessage(err, "聊天请求失败"));
      setCurrentQuestion("");
      setCurrentSteps([]);
      chatStepsRef.current = [];
      setChatLoading(false);
      chatLoadingRef.current = false;
    }
  }

  // ---------------- URL 写入（替代旧的动作函数） ----------------

  function goToProjectList() {
    navigate({ view: "projects" });
  }

  function goToProject(projectId: string) {
    navigate({ view: "project-overview", projectId });
  }

  function goToProjectSection(section: string) {
    if (!selectedProjectID) return;
    if (section === "mcp") navigate({ view: "project-mcp", projectId: selectedProjectID });
    else if (section === "chat") navigate({ view: "project-chat", projectId: selectedProjectID });
    else if (section === "console") navigate({ view: "project-console", projectId: selectedProjectID });
    else if (section === "tools") navigate({ view: "project-tools", projectId: selectedProjectID });
    else navigate({ view: "project-overview", projectId: selectedProjectID });
  }

  function clearChat() {
    setChatExchanges([]);
    setCurrentSteps([]);
    chatStepsRef.current = [];
    setCurrentQuestion("");
    setChatError("");
  }


  function selectServerFromUI(nodeletID: string) {
    if (!selectedProjectID) return;
    replace({ view: "project-overview", projectId: selectedProjectID, serverId: nodeletID });
  }

  function selectContainerFromUI(nodeletID: string, containerID: string) {
    if (!selectedProjectID) return;
    replace({ view: "project-overview", projectId: selectedProjectID, serverId: nodeletID, containerId: containerID });
  }

  // 切换服务器展开/折叠
  async function toggleServer(nodeletID: string) {
    const next = new Set(expandedServers);
    if (next.has(nodeletID)) {
      next.delete(nodeletID);
      setExpandedServers(next);
      // 如果折叠的是 URL 中指定的服务器，回到项目概览
      if (urlServerId === nodeletID) {
        replace({ view: "project-overview", projectId: selectedProjectID });
      }
    } else {
      next.add(nodeletID);
      setExpandedServers(next);
      if (!containers[nodeletID]) {
        loadContainers(selectedProjectID, nodeletID);
      }
      // 同步到 URL
      selectServerFromUI(nodeletID);
    }
  }

  // ---------------- Effect 级联：URL → 数据加载 ----------------

  // Effect A: 项目进入/离开
  React.useEffect(() => {
    if (selectedProjectID && selectedProjectID !== lastLoadedProjectRef.current) {
      // 进入新项目
      lastLoadedProjectRef.current = selectedProjectID;
      setServers([]);
      setContainers({});
      setExpandedServers(new Set());
      setContainerDetail(undefined);
      closeLogStream();
      setLogs([]);
      loadProjectServers(selectedProjectID);
    }
    if (!selectedProjectID && lastLoadedProjectRef.current) {
      // 离开项目
      lastLoadedProjectRef.current = "";
      setServers([]);
      setContainers({});
      setExpandedServers(new Set());
      setContainerDetail(undefined);
      closeLogStream();
      setLogs([]);
    }
  }, [selectedProjectID]);

  // Effect B: URL 中有 server 时，展开并加载容器
  React.useEffect(() => {
    if (!urlServerId || servers.length === 0) return;

    const server = servers.find((s) => s.nodelet.id === urlServerId);
    if (!server) {
      // URL 中的 server 不在当前项目里，回退
      replace({ view: "project-overview", projectId: selectedProjectID });
      return;
    }

    setExpandedServers((prev) => {
      if (prev.has(urlServerId)) return prev;
      return new Set(prev).add(urlServerId);
    });

    if (!containers[urlServerId]) {
      loadContainers(selectedProjectID, urlServerId);
    }
  }, [urlServerId, servers, selectedProjectID]);

  // Effect C: URL 中有 container 时，选中并加载详情+日志
  React.useEffect(() => {
    if (!urlContainerId || !urlServerId) return;
    // 避免重复选中同一个容器
    if (containerDetail && containerDetail.container.id === urlContainerId) return;

    const serverContainers = containers[urlServerId];
    if (!serverContainers) return; // 容器列表还没加载

    const container = serverContainers.find((c) => c.id === urlContainerId);
    if (!container) {
      // URL 中的 container 不存在，去掉
      replace({ view: "project-overview", projectId: selectedProjectID, serverId: urlServerId });
      return;
    }

    selectContainer(selectedProjectID, urlServerId, urlContainerId);
  }, [urlContainerId, urlServerId, containers, selectedProjectID]);

  // Effect D: 指定了 server 但没有 container → 自动选第一个容器
  React.useEffect(() => {
    if (!urlServerId || urlContainerId) return;
    if (selectedContainerID) return; // 已有选中

    const serverContainers = containers[urlServerId];
    if (!serverContainers || serverContainers.length === 0) return;

    const first = serverContainers[0];
    replace({
      view: "project-overview",
      projectId: selectedProjectID,
      serverId: urlServerId,
      containerId: first.id,
    });
  }, [urlServerId, urlContainerId, containers, selectedProjectID]);

  // Effect E: 进入项目概览且无 server → 自动展开首台服务器
  React.useEffect(() => {
    if (route.view !== "project-overview") return;
    if (urlServerId) return;
    if (servers.length === 0 || serversLoading) return;

    const first = servers[0];
    replace({
      view: "project-overview",
      projectId: selectedProjectID,
      serverId: first.nodelet.id,
    });
  }, [route.view, urlServerId, servers, serversLoading, selectedProjectID]);

  // ---------------- 基础 Effects ----------------

  React.useEffect(() => {
    fetchProjects();
  }, []);

  React.useEffect(() => {
    return () => closeLogStream();
  }, [flushLogs]);

  // 自动滚动由 ContainerLogs 内部的 IntersectionObserver + MutationObserver 处理

  // ---- 标题 ----
  const selectedProject = projects.find((p) => p.id === selectedProjectID);
  const isProjectRoute = Boolean(selectedProjectID);

  React.useEffect(() => {
    const parts: string[] = [];
    if (selectedProject) parts.push(selectedProject.name);
    if (selectedProject && projectSection === "mcp") parts.push("MCP 管理");
    if (selectedProject && projectSection === "tools") parts.push("工具管理");
    if (selectedProject && projectSection === "chat") parts.push("助手");
    if (selectedProject && projectSection === "console") parts.push("控制台");
    document.title = parts.length > 0 ? `${parts.join(" · ")} — Oops` : "Oops";
  }, [selectedProject, projectSection]);

  // ---- Render ----
  return (
    <div className={`shell ${sidebarCollapsed ? "sidebar-collapsed" : ""}`}>
      <a href="#main-content" className="skip-link">
        跳到主内容
      </a>
      <Header activeNav={activeNav} onNavChange={() => {}} />
      <SideRail
        activeNav={isProjectRoute ? projectSection : activeNav}
        collapsed={sidebarCollapsed}
        variant={isProjectRoute ? "project" : "global"}
        onCollapsedChange={setSidebarCollapsed}
        onNavChange={
          isProjectRoute
            ? goToProjectSection
            : (id: string) => {
                if (id === "console") navigate({ view: "console" });
                else if (id === "projects") navigate({ view: "projects" });
              }
        }
      />

      <main id="main-content" className="content">
        {activeNav === "console" && !selectedProject && (
          <section className="workspace-card">
            <ConsolePanel />
          </section>
        )}

        {activeNav === "projects" && !selectedProject && (
          <section className="workspace-card">
            <div className="workspace-head">
              <div>
                <h1>项目</h1>
                <p>管理运维项目，查看服务器、容器与服务状态。</p>
              </div>
            </div>
            <ProjectsView
              projects={projects}
              loading={projectsLoading}
              error={projectsError}
              onSelect={goToProject}
              onRefresh={fetchProjects}
            />
          </section>
        )}

        {activeNav === "projects" && selectedProject && projectSection === "overview" && (
          <ProjectDetailView
            project={selectedProject}
            servers={servers}
            serversLoading={serversLoading}
            serverError={serverError}
            containers={containers}
            containersLoading={containersLoading}
            selectedNodeletID={selectedNodeletID}
            selectedContainerID={selectedContainerID}
            containerDetail={containerDetail}
            containerDetailLoading={detailLoading}
            containerDetailError={detailError}
            health={health}
            healthLoading={healthLoading}
            expandedServers={expandedServers}
            logs={logs}
            logsLoading={logsLoading}
            logsError={logsError}
            autoScroll={autoScroll}
            onBack={goToProjectList}
            onToggleServer={toggleServer}
            onSelectContainer={selectContainerFromUI}
            onServersChanged={() => loadProjectServers(selectedProjectID)}
            onHealthCheck={checkHealth}
            onAutoScrollChange={setAutoScroll}
            onClearLogs={() => {
              flushLogs.cancel();
              logBuffer.current = [];
              setLogs([]);
            }}
            logsPanelRef={logsPanel}
            onMCPChanged={() => {
              if (selectedProjectID && urlServerId && urlContainerId) {
                selectContainer(selectedProjectID, urlServerId, urlContainerId);
              }
            }}
          />
        )}

        {activeNav === "projects" && selectedProject && projectSection === "mcp" && (
          <section className="workspace-card">
            <div className="workspace-head">
              <div>
                <h1>MCP 管理</h1>
                <p>管理 LLM Agent 的 MCP 工具连接，支持 MySQL、Redis、PostgreSQL 等社区 MCP 服务器。</p>
              </div>
            </div>
            <MCPView />
          </section>
        )}

        {activeNav === "projects" && selectedProject && projectSection === "tools" && (
          <section className="workspace-card">
            <div className="workspace-head">
              <div>
                <h1>工具管理</h1>
                <p>管理 LLM Agent 可用的工具。关闭某个工具后，Agent 将无法调用它。</p>
              </div>
            </div>
            <ToolsView />
          </section>
        )}

        {activeNav === "projects" && selectedProject && projectSection === "chat" && (
          <section className="workspace-card chat-workspace">
            <ChatView
              chatExchanges={chatExchanges}
              currentSteps={currentSteps}
              currentQuestion={currentQuestion}
              chatInput={chatInput}
              chatLoading={chatLoading}
              chatError={chatError}
              onInputChange={setChatInput}
              onSend={() => sendChat()}
              onClear={clearChat}
            />
          </section>
        )}

        {activeNav === "projects" && selectedProject && projectSection === "console" && (
          <section className="workspace-card">
            <ConsolePanel />
          </section>
        )}
      </main>
    </div>
  );
}
