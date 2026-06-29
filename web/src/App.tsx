import React from "react";
import type {
  Project,
  ServerWithNodelet,
  ContainerWithType,
  ContainerDetail as ContainerDetailType,
  HealthResult,
  LogEntry,
  StepEvent,
  ChatExchange,
  SessionInfo,
  SessionDetail,
} from "./types";
import { MAX_LOGS, LOG_FLUSH_MS, LOG_MAX_WAIT_MS } from "./types";
import { usePathRouter } from "./hooks/usePathRouter";
import { pageConfig } from "./lib/config";
import { apiRequest, getErrorMessage } from "./lib/api";
import { projectPaths, serverPaths, sessionPaths } from "./lib/paths";
import { Header } from "./components/Header";
import { SideRail } from "./components/SideRail";
import { ProjectsView } from "./components/ProjectsView";
import { ProjectDetailView } from "./components/ProjectDetailView";
import { ChatView } from "./components/ChatView";
import { MCPView } from "./components/MCPView";
import { ToolsView } from "./components/ToolsView";
import { ConsolePanel } from "./components/ConsolePanel";
import { LoginPage } from "./components/LoginPage";
import "./styles.css";

export function App() {
  // ===================================================================
  // 所有 Hooks 必须在顶层调用（React 规则），条件 return 放在最后。
  // ===================================================================

  // ---- 登录状态 ----
  const hasInjectedUser = pageConfig.authProvider !== "none" && !!pageConfig.user;
  const [authenticated, setAuthenticated] = React.useState(hasInjectedUser);
  const [authChecked, setAuthChecked] = React.useState(hasInjectedUser);

  React.useEffect(() => {
    if (!hasInjectedUser) {
      fetch("/api/auth/me")
        .then((r) => { setAuthenticated(r.ok); setAuthChecked(true); })
        .catch(() => { setAuthenticated(false); setAuthChecked(true); });
    }
  }, []);

  // ---- 路由（始终调用，即使未登录也解析路径） ----
  const { route, navigate, replace } = usePathRouter();

  const activeNav = route.view === "console" ? "console" : "projects";
  const selectedProjectID =
    route.view === "projects" || route.view === "console"
      ? ""
      : route.projectId || "";
  const projectSection: string =
    route.view === "project-mcp" ? "mcp" :
    route.view === "project-chat" ? "chat" :
    route.view === "project-console" ? "console" :
    route.view === "project-tools" ? "tools" :
    route.view === "project-overview" ? "overview" :
    "overview";
  const [selectedNodeletID, setSelectedNodeletID] = React.useState("");
  const [selectedContainerID, setSelectedContainerID] = React.useState("");

  const lastLoadedProjectRef = React.useRef("");

  // 侧栏折叠
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
  const [containersLoading, setContainersLoading] = React.useState<Set<string>>(new Set());
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
  const [sessionId, setSessionId] = React.useState<string>(() => {
    return localStorage.getItem("oops_session_id") || "";
  });
  const [sessionLoaded, setSessionLoaded] = React.useState(false);
  const [sessions, setSessions] = React.useState<SessionInfo[]>([]);

  // ---- 日志缓冲区 ----
  const flushTimerRef = React.useRef<ReturnType<typeof setTimeout> | null>(null);
  const flushFirstRef = React.useRef<number>(0);

  // 使用 ref 持有最新的 doFlush 以避免过期闭包
  const doFlushRef = React.useRef(() => {
    if (logBuffer.current.length === 0) return;
    const nextLogs = logBuffer.current;
    logBuffer.current = [];
    setLogs((current) => [...current, ...nextLogs].slice(-MAX_LOGS));
  });
  doFlushRef.current = () => {
    if (logBuffer.current.length === 0) return;
    const nextLogs = logBuffer.current;
    logBuffer.current = [];
    setLogs((current) => [...current, ...nextLogs].slice(-MAX_LOGS));
  };

  type FlushFn = (() => void) & { cancel: () => void };

  const flushLogs = React.useCallback(() => {
    const now = Date.now();
    if (flushFirstRef.current === 0) flushFirstRef.current = now;
    if (now - flushFirstRef.current >= LOG_MAX_WAIT_MS) {
      if (flushTimerRef.current) { clearTimeout(flushTimerRef.current); flushTimerRef.current = null; }
      flushFirstRef.current = 0;
      doFlushRef.current();
      return;
    }
    if (flushTimerRef.current) clearTimeout(flushTimerRef.current);
    flushTimerRef.current = setTimeout(() => {
      flushTimerRef.current = null;
      flushFirstRef.current = 0;
      doFlushRef.current();
    }, LOG_FLUSH_MS);
  }, []) as FlushFn;

  flushLogs.cancel = () => {
    if (flushTimerRef.current) { clearTimeout(flushTimerRef.current); flushTimerRef.current = null; }
    flushFirstRef.current = 0;
  };

  function closeLogStream() {
    if (logEventSource.current) {
      logEventSource.current.close();
      logEventSource.current = null;
    }
    flushLogs.cancel();
    logBuffer.current = [];
  }

  // ---- 会话加载 ----
  React.useEffect(() => {
    if (!authenticated || !sessionId || sessionLoaded) return;
    let cancelled = false;
    async function load() {
      try {
        const detail = await apiRequest<SessionDetail>(
          sessionPaths(sessionId).get + "?include_messages=true"
        );
        if (!cancelled && detail?.id) {
          const exchanges: ChatExchange[] = [];
          let currentQuestion = "";
          let currentSteps: StepEvent[] = [];
          for (const msg of detail.messages) {
            if (msg.role === "user") {
              if (currentQuestion) {
                exchanges.push({ question: currentQuestion, steps: currentSteps });
              }
              currentQuestion = msg.content;
              currentSteps = [];
            } else if (msg.role === "assistant") {
              currentSteps.push({ type: "answer", content: msg.content });
            } else if (msg.role === "thinking") {
              currentSteps.push({ type: "thinking", content: msg.content });
            } else if (msg.role === "tool_call") {
              currentSteps.push({
                type: "tool_call",
                content: msg.content,
                toolCallId: msg.toolCallId,
                toolName: msg.toolName,
                toolArgs: msg.toolArgs,
              });
            } else if (msg.role === "tool") {
              currentSteps.push({
                type: "tool_result",
                content: msg.content,
                toolCallId: msg.toolCallId,
                toolName: msg.toolName,
              });
            }
          }
          if (currentQuestion) {
            const answer = currentSteps.find((s) => s.type === "answer");
            exchanges.push({
              question: currentQuestion,
              steps: currentSteps,
              answer: answer?.content,
            });
          }
          setChatExchanges(exchanges);
          setSessionLoaded(true);
          loadSessions();
        }
      } catch {
        if (!cancelled) {
          localStorage.removeItem("oops_session_id");
          setSessionId("");
        }
      }
    }
    load();
    return () => { cancelled = true; };
  }, [authenticated, sessionId, sessionLoaded]);

  // ---------------- 数据获取 ----------------

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
    setContainersLoading((prev) => new Set(prev).add(nodeletID));
    try {
      const data = await apiRequest<ContainerWithType[]>(serverPaths(projectID, nodeletID).containers);
      setContainers((prev) => ({ ...prev, [nodeletID]: data }));
      setServers((prev) => prev.map((sw) => (
        sw.nodelet.id === nodeletID
          ? { ...sw, host: { ...sw.host, available: true }, error: "" }
          : sw
      )));
    } catch (err) {
      const message = getErrorMessage(err, "读取容器列表失败");
      setContainers((prev) => ({ ...prev, [nodeletID]: [] }));
      setServers((prev) => prev.map((sw) => (
        sw.nodelet.id === nodeletID
          ? { ...sw, host: { ...sw.host, available: false }, error: message }
          : sw
      )));
    } finally {
      setContainersLoading((prev) => {
        const next = new Set(prev);
        next.delete(nodeletID);
        return next;
      });
    }
  }

  async function selectContainer(projectId: string, nodeletID: string, containerID: string) {
    closeLogStream();
    setDetailError("");
    setHealth(undefined);

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
        body: JSON.stringify({ session_id: sessionId || undefined, question: q }),
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
              if (evt.type === "session" && evt.content) {
                setSessionId(evt.content);
                localStorage.setItem("oops_session_id", evt.content);
                continue;
              }
              chatStepsRef.current = [...chatStepsRef.current, evt];
              setCurrentSteps(chatStepsRef.current);
            } catch { /* skip */ }
          }
        }
      }

      const steps = chatStepsRef.current;
      const answer = steps.find((s) => s.type === "answer");
      const errStep = steps.find((s) => s.type === "error");
      setChatExchanges((prev) => [...prev, { question: q, steps, answer: answer?.content, error: errStep?.content }]);
      chatStepsRef.current = [];
      setCurrentSteps([]);
      setCurrentQuestion("");
      setChatLoading(false);
      chatLoadingRef.current = false;
      loadSessions();
    } catch (err) {
      setChatError(getErrorMessage(err, "聊天请求失败"));
      setCurrentQuestion("");
      setCurrentSteps([]);
      chatStepsRef.current = [];
      setChatLoading(false);
      chatLoadingRef.current = false;
    }
  }

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

  function startNewChat() {
    localStorage.removeItem("oops_session_id");
    setChatExchanges([]);
    setCurrentSteps([]);
    chatStepsRef.current = [];
    setCurrentQuestion("");
    setChatError("");
    setSessionId("");
    setSessionLoaded(false);
  }

  async function loadSessions() {
    try {
      const list = await apiRequest<SessionInfo[]>(
        "/api/sessions" + (selectedProjectID ? `?project_id=${encodeURIComponent(selectedProjectID)}` : "")
      );
      setSessions(list || []);
    } catch { /* 会话列表加载失败不影响主流程 */ }
  }

  function clearChat() {
    if (sessionId) {
      fetch(sessionPaths(sessionId).delete, { method: "DELETE" }).catch(() => {});
      localStorage.removeItem("oops_session_id");
    }
    setChatExchanges([]);
    setCurrentSteps([]);
    chatStepsRef.current = [];
    setCurrentQuestion("");
    setChatError("");
    setSessionId("");
    setSessionLoaded(false);
    setSessions((prev) => prev.filter((s) => s.id !== sessionId));
  }

  React.useEffect(() => {
    if (!authenticated) return;
    if (sessionId && sessionLoaded) {
      startNewChat();
    }
    loadSessions();
  }, [authenticated, selectedProjectID]);

  function selectServerFromUI(nodeletID: string) {
    if (!selectedProjectID) return;
    setSelectedNodeletID(nodeletID);
    setSelectedContainerID("");
  }

  function selectContainerFromUI(nodeletID: string, containerID: string) {
    if (!selectedProjectID) return;
    setSelectedNodeletID(nodeletID);
    setSelectedContainerID(containerID);
  }

  function toggleServer(nodeletID: string) {
    if (expandedServers.has(nodeletID)) {
      // 折叠：直接从 expandedServers 移除
      setExpandedServers((prev) => {
        const next = new Set(prev);
        next.delete(nodeletID);
        return next;
      });
      // 如果折叠的是当前选中的 server，清除选中状态
      if (selectedNodeletID === nodeletID) {
        setSelectedNodeletID("");
        setSelectedContainerID("");
      }
    } else {
      // 展开并选中
      setExpandedServers((prev) => new Set(prev).add(nodeletID));
      if (!containers[nodeletID]) {
        loadContainers(selectedProjectID, nodeletID);
      }
      selectServerFromUI(nodeletID);
    }
  }

  // ---------------- Effect 级联：状态 → 数据加载 ----------------

  // Effect A: 项目进入/离开
  React.useEffect(() => {
    if (!authenticated) return;
    if (selectedProjectID && selectedProjectID !== lastLoadedProjectRef.current) {
      lastLoadedProjectRef.current = selectedProjectID;
      setSelectedNodeletID("");
      setSelectedContainerID("");
      setServers([]);
      setContainers({});
      setExpandedServers(new Set());
      setContainerDetail(undefined);
      closeLogStream();
      setLogs([]);
      loadProjectServers(selectedProjectID);
    }
    if (!selectedProjectID && lastLoadedProjectRef.current) {
      lastLoadedProjectRef.current = "";
      setSelectedNodeletID("");
      setSelectedContainerID("");
      setServers([]);
      setContainers({});
      setExpandedServers(new Set());
      setContainerDetail(undefined);
      closeLogStream();
      setLogs([]);
    }
  }, [authenticated, selectedProjectID]);

  // Effect C: 选中 container → 加载详情+日志
  React.useEffect(() => {
    if (!authenticated || !selectedNodeletID || !selectedContainerID) return;
    if (containerDetail && containerDetail.container.id === selectedContainerID) return;
    selectContainer(selectedProjectID, selectedNodeletID, selectedContainerID);
  }, [authenticated, selectedNodeletID, selectedContainerID, selectedProjectID]);

  // Effect D: 选中了 server 但没有 container → 自动选第一个容器
  React.useEffect(() => {
    if (!authenticated || !selectedNodeletID || selectedContainerID) return;

    const serverContainers = containers[selectedNodeletID];
    if (!serverContainers || serverContainers.length === 0) return;

    setSelectedContainerID(serverContainers[0].id);
  }, [authenticated, selectedNodeletID, selectedContainerID, containers, selectedProjectID]);

  // Effect E: 进入项目概览且无展开的 server → 自动展开首台服务器并选中
  React.useEffect(() => {
    if (!authenticated || route.view !== "project-overview") return;
    if (servers.length === 0 || serversLoading) return;
    if (expandedServers.size > 0) return;

    const first = servers[0];
    setExpandedServers(new Set([first.nodelet.id]));
    setSelectedNodeletID(first.nodelet.id);
    setSelectedContainerID("");
    if (!containers[first.nodelet.id]) {
      loadContainers(selectedProjectID, first.nodelet.id);
    }
  }, [authenticated, route.view, servers, serversLoading, expandedServers.size, selectedProjectID, containers]);

  // ---------------- 基础 Effects ----------------

  React.useEffect(() => {
    if (!authenticated) return;
    fetchProjects();
  }, [authenticated]);

  React.useEffect(() => {
    return () => closeLogStream();
    // flushLogs is useCallback([], []) — stable across renders
  }, []);

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

  // ===================================================================
  // 条件渲染（必须在所有 Hooks 之后）
  // ===================================================================

  if (!authChecked) {
    return <div className="login-page"><p>加载中...</p></div>;
  }
  if (!authenticated) {
    return <LoginPage />;
  }

  // ---- Render ----
  return (
    <div className={`shell ${sidebarCollapsed ? "sidebar-collapsed" : ""}`}>
      <a href="#main-content" className="skip-link">
        跳到主内容
      </a>
      <Header activeNav={activeNav} onNavChange={(id: string) => {
        if (id === "console") navigate({ view: "console" });
        // 全局视图下的 chat/projects 都导航到项目列表
        else navigate({ view: "projects" });
      }} />
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
              if (selectedProjectID && selectedNodeletID && selectedContainerID) {
                selectContainer(selectedProjectID, selectedNodeletID, selectedContainerID);
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
              sessionId={sessionId}
              sessions={sessions}
              onInputChange={setChatInput}
              onSend={() => sendChat()}
              onClear={clearChat}
              onNewChat={startNewChat}
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
