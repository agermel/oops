import React from "react";
import type {
  StepEvent,
  ChatExchange,
  SessionDetail,
  MCPConnectionStatus,
  ProjectMCPConnection,
  MCPPrefill,
} from "./types";
import { SESSION_STORAGE_KEY } from "./types";
import { useQueryClient } from "@tanstack/react-query";
import { useModal } from "./hooks/useModal";
import { useSet } from "./hooks/useSet";
import { useLogStream } from "./hooks/useLogStream";
import { usePathRouter } from "./hooks/usePathRouter";
import { useProjects } from "./hooks/useProjects";
import {
  useProjectServers,
  useNodeletStatus,
  useProjectContainerOptions,
} from "./hooks/useServers";
import { useSkills } from "./hooks/useSkills";
import { useSessions } from "./hooks/useSessions";
import { queryKeys } from "./hooks/queries";
import { pageConfig } from "./lib/config";
import { apiRequest, getErrorMessage } from "./lib/api";
import { projectPaths, sessionPaths, authPaths, chatPaths } from "./lib/paths";
import { deserializeSessionMessages } from "./lib/session";
import { shouldAutoExpandFirstServer } from "./lib/serverTreeState";
import { Header } from "./components/Header";
import { SideRail } from "./components/SideRail";
import { ProjectsView } from "./components/ProjectsView";
import { ProjectDetailView } from "./components/ProjectDetailView";
import { NodeletManagementView } from "./components/NodeletManagementView";
import { ChatView } from "./components/ChatView";
import { MCPFormModal } from "./components/MCPFormModal";
import { ToolsView } from "./components/ToolsView";
import { SkillsView } from "./components/SkillsView";
import { ConsolePanel } from "./components/ConsolePanel";
import { LoginPage } from "./components/LoginPage";
import { SetupPage } from "./components/SetupPage";
import "./styles.css";

export function App() {
  // ===================================================================
  // 所有 Hooks 必须在顶层调用（React 规则），条件 return 放在最后。
  // ===================================================================

  // ---- 登录状态 ----
  const hasInjectedUser = pageConfig.authProvider !== "none" && !!pageConfig.user;
  // needsSetup 可能由服务端注入（生产模式），也可能未定义（Vite dev 模式）
  const injectedNeedsSetup = pageConfig.needsSetup === true;
  const [needsSetup, setNeedsSetup] = React.useState(injectedNeedsSetup);
  const [authenticated, setAuthenticated] = React.useState(hasInjectedUser && !injectedNeedsSetup);
  const [authChecked, setAuthChecked] = React.useState(hasInjectedUser || injectedNeedsSetup);

  React.useEffect(() => {
    if (hasInjectedUser) return; // 服务端已注入用户 → 已登录

    if (injectedNeedsSetup) {
      // 服务端已告知需要设置（生产模式）
      return;
    }

    // Dev 模式或未注入时：并行检查 /api/auth/status 和 /api/auth/me
    Promise.all([
      fetch(authPaths.status).then(r => r.json()).catch(() => ({ setup: false })),
      fetch(authPaths.me).then(r => ({ ok: r.ok })).catch(() => ({ ok: false })),
    ]).then(([status, me]) => {
      if (status.setup === false) {
        setNeedsSetup(true);
      } else {
        setAuthenticated(me.ok);
      }
      setAuthChecked(true);
    });
  }, []);

  // ---- 路由（始终调用，即使未登录也解析路径） ----
  const { route, navigate, replace } = usePathRouter();
  const routeViewRef = React.useRef(route.view);
  routeViewRef.current = route.view;

  const activeNav = route.view === "servers" ? "servers" : route.view === "console" ? "console" : "projects";
  const selectedProjectID =
    route.view === "projects" || route.view === "servers" || route.view === "console"
      ? ""
      : route.projectId || "";
  const projectSection: string =
    route.view === "project-chat" ? "chat" :
    route.view === "project-console" ? "console" :
    route.view === "project-tools" ? "tools" :
    route.view === "project-skills" ? "skills" :
    route.view === "project-overview" ? "overview" :
    "overview";
  const [selectedNodeletID, setSelectedNodeletID] = React.useState("");
  const [selectedContainerID, setSelectedContainerID] = React.useState("");

  const lastLoadedProjectRef = React.useRef("");
  const autoExpandFirstServerRef = React.useRef(false);

  // 侧栏折叠
  const [sidebarCollapsed, setSidebarCollapsed] = React.useState(false);

  // MCP 新建/编辑连接模态框（App 级，供 workspace head 按钮和快捷卡片共用）
  const mcpForm = useModal<MCPConnectionStatus>();
  const [mcpPrefill, setMCPPrefill] = React.useState<MCPPrefill | null>(null);

  // MCP 连接选择状态 —— 当用户在左侧栏点击 MCP 连接时设置
  const [selectedMCPConnectionID, setSelectedMCPConnectionID] = React.useState("");

  // ---- 数据域 hooks（TanStack Query 管理） ----
  const {
    data: projects = [],
    isLoading: projectsLoading,
    error: projectsQueryError,
  } = useProjects();
  const projectsError = projectsQueryError ? getErrorMessage(projectsQueryError, "读取项目列表失败") : "";

  const selectedProjectID_clean = selectedProjectID;
  const {
    data: rawServers = [],
    isLoading: serversLoading,
    error: serversQueryError,
  } = useProjectServers(selectedProjectID_clean);
  const serverError = serversQueryError ? getErrorMessage(serversQueryError, "读取服务器列表失败") : "";

  const { data: nodeletStatusItems = [] } = useNodeletStatus();
  const { data: skills = [] } = useSkills();
  const {
    data: sessions = [],
  } = useSessions(selectedProjectID_clean || "");

  const queryClient = useQueryClient();

  // agentMeta: 从 skills 派生
  const agentMeta = React.useMemo(() => {
    const meta: Record<string, { label: string; iconName: string; color: string }> = {};
    for (const s of skills) {
      if (s.enabled) {
        meta[s.name] = { label: s.label, iconName: s.icon, color: s.color };
      }
    }
    return meta;
  }, [skills]);

  // 将 Prober 状态合并到服务器列表（原 mergeProberStatus 函数，现用 useMemo）
  const servers = React.useMemo(() => {
    if (rawServers.length === 0) return rawServers;
    const statusMap = new Map(nodeletStatusItems.map((s) => [s.nodelet.id, s]));
    return rawServers.map((sw) => {
      const si = statusMap.get(sw.nodelet.id);
      if (!si) return sw;
      return {
        ...sw,
        host: { ...sw.host, available: si.status === "healthy" },
        error: si.status === "healthy" ? undefined : (si.error || sw.error),
      };
    });
  }, [rawServers, nodeletStatusItems]);

  const {
    data: mcpContainerOptions = [],
    isLoading: mcpContainerOptionsLoading,
  } = useProjectContainerOptions(selectedProjectID_clean, servers, authenticated && !!selectedProjectID_clean && mcpForm.open);

  // ---- 服务器展开/折叠 ----
  const expandedServers = useSet();

  // ---- 日志流（useLogStream hook 管理 EventSource 生命周期） ----
  const { logs, loading: logsLoading, error: logsError, autoScroll, setAutoScroll, panelRef: logsPanel, clear: clearLogs } = useLogStream(
    selectedProjectID, selectedNodeletID, selectedContainerID
  );

  // ---- 聊天状态 ----
  const [chatExchanges, setChatExchanges] = React.useState<ChatExchange[]>([]);
  const [currentSteps, setCurrentSteps] = React.useState<StepEvent[]>([]);
  const [chatInput, setChatInput] = React.useState("");
  const [currentQuestion, setCurrentQuestion] = React.useState("");
  const [chatLoading, setChatLoading] = React.useState(false);
  const [chatError, setChatError] = React.useState("");
  const chatLoadingRef = React.useRef(false);
  const chatStepsRef = React.useRef<StepEvent[]>([]);
  const chatAbortRef = React.useRef<AbortController | null>(null);
  const [sessionId, setSessionId] = React.useState<string>(() => {
    return localStorage.getItem(SESSION_STORAGE_KEY) || "";
  });
  const sessionIdRef = React.useRef(sessionId);
  sessionIdRef.current = sessionId;
  const [sessionLoaded, setSessionLoaded] = React.useState(false);
  const sessionLoadedRef = React.useRef(sessionLoaded);
  sessionLoadedRef.current = sessionLoaded;
  const [agentType, setAgentType] = React.useState<string>("");
  const [maxStep, setMaxStep] = React.useState<number>(0);
  const [tokenStats, setTokenStats] = React.useState<{ tokens: number; trimmed: number } | null>(null);

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
          setChatExchanges(deserializeSessionMessages(detail.messages));
          setSessionLoaded(true);
          queryClient.invalidateQueries({ queryKey: queryKeys.sessions.all(selectedProjectID || undefined) });
        }
      } catch {
        if (!cancelled) {
          localStorage.removeItem(SESSION_STORAGE_KEY);
          setSessionId("");
        }
      }
    }
    load();
    return () => { cancelled = true; };
  }, [authenticated, sessionId, sessionLoaded]);

  // Prober 状态通过 useNodeletStatus() 的 refetchInterval: 30_000 自动轮询，
  // servers 的合并通过 useMemo 完成（见上方），无需额外的 effect。

  // toggleServer 只管理展开状态，容器列表由 ServerTree 内部查询加载。

  async function sendChat(question?: string) {
    const q = (question ?? chatInput).trim();
    if (!q) return;
    // 自愈：若 UI 已不显示 loading 但 ref 泄漏（如导航中途离开聊天页），则重置
    if (!chatLoading) chatLoadingRef.current = false;
    if (chatLoadingRef.current) return;

    // Abort any in-flight request
    chatAbortRef.current?.abort();
    const controller = new AbortController();
    chatAbortRef.current = controller;

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
        : chatPaths.default;
      const resp = await fetch(url, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ session_id: sessionId || undefined, question: q }),
        signal: controller.signal,
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
          if (line.startsWith("data: [DONE]")) { streamDone = true; break; }
          if (line.startsWith("data: ")) {
            try {
              const evt: StepEvent = JSON.parse(line.slice(6));
              if (evt.type === "session" && evt.content) {
                setSessionId(evt.content);
                setSessionLoaded(true);
                localStorage.setItem(SESSION_STORAGE_KEY, evt.content);
                if (evt.agentType) setAgentType(evt.agentType);
                if (evt.maxStep) setMaxStep(evt.maxStep);
                continue;
              }
              if (evt.type === "stats") {
                setTokenStats({ tokens: evt.tokens || 0, trimmed: evt.trimmed || 0 });
                continue;
              }
              chatStepsRef.current = [...chatStepsRef.current, evt];
              setCurrentSteps(chatStepsRef.current);
            } catch { /* skip */ }
          }
        }
      }

      const steps = chatStepsRef.current;
      const answerContents = steps.filter((s) => s.type === "answer").map((s) => s.content);
      const fullAnswer = answerContents.length > 0 ? answerContents.join("") : undefined;
      const errStep = steps.filter((s) => s.type === "error").pop();
      setChatExchanges((prev) => [...prev, { question: q, steps, answer: fullAnswer, error: errStep?.content }]);
      chatStepsRef.current = [];
      setCurrentSteps([]);
      setCurrentQuestion("");
      setChatLoading(false);
      chatLoadingRef.current = false;
      queryClient.invalidateQueries({ queryKey: queryKeys.sessions.all(selectedProjectID || undefined) });
    } catch (err) {
      const errorMessage = getErrorMessage(err, "聊天请求失败");
      // 保存失败的 exchange，让用户能看到自己提的问题和错误信息；
      // 不再设 chatError，避免与 exchange 内的 error 重复显示。
      setChatExchanges((prev) => [...prev, { question: q, steps: [], error: errorMessage }]);
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
    if (section === "chat") navigate({ view: "project-chat", projectId: selectedProjectID });
    else if (section === "console") navigate({ view: "project-console", projectId: selectedProjectID });
    else if (section === "tools") navigate({ view: "project-tools", projectId: selectedProjectID });
    else if (section === "skills") navigate({ view: "project-skills", projectId: selectedProjectID });
    else navigate({ view: "project-overview", projectId: selectedProjectID });
  }

  function startNewChat() {
    chatAbortRef.current?.abort();
    localStorage.removeItem(SESSION_STORAGE_KEY);
    setChatExchanges([]);
    setCurrentSteps([]);
    chatStepsRef.current = [];
    setCurrentQuestion("");
    setChatError("");
    setSessionId("");
    setSessionLoaded(false);
    setAgentType("");
    setMaxStep(0);
    setTokenStats(null);
    setChatLoading(false);
    chatLoadingRef.current = false;
  }

  async function switchSession(id: string) {
    if (chatLoadingRef.current) return; // 正在流式传输中不切换
    setChatLoading(true); // 用 chatLoading 指示会话切换中
    setChatError("");
    try {
      const detail = await apiRequest<SessionDetail>(
        sessionPaths(id).get + "?include_messages=true"
      );
      if (detail?.id) {
        setChatExchanges(deserializeSessionMessages(detail.messages));
        setCurrentSteps([]);
        chatStepsRef.current = [];
        setCurrentQuestion("");
        setChatError("");
        setSessionId(id);
        localStorage.setItem(SESSION_STORAGE_KEY, id);
        setSessionLoaded(true);
        setAgentType("");
        setMaxStep(0);
        setTokenStats(null);
        queryClient.invalidateQueries({ queryKey: queryKeys.sessions.all(selectedProjectID || undefined) });
      }
    } catch (err) {
      setChatError(getErrorMessage(err, "加载会话失败"));
    } finally {
      setChatLoading(false);
    }
  }


  async function clearChat() {
    chatAbortRef.current?.abort();
    let deleted = false;
    if (sessionId) {
      try {
        const resp = await fetch(sessionPaths(sessionId).delete, { method: "DELETE" });
        if (resp.ok || resp.status === 404) deleted = true;
      } catch {
        // 网络错误：仍然清除本地状态，但保留 sessionId 以便重试
      }
    }
    if (sessionId && !deleted) {
      // 删除失败：不清除 localStorage 和 sessionId，用户可重试
      setChatError("清除会话失败，请重试");
      return;
    }
    localStorage.removeItem(SESSION_STORAGE_KEY);
    setChatExchanges([]);
    setCurrentSteps([]);
    chatStepsRef.current = [];
    setCurrentQuestion("");
    setChatError("");
    setSessionId("");
    setSessionLoaded(false);
    queryClient.invalidateQueries({ queryKey: queryKeys.sessions.all(selectedProjectID || undefined) });
    setAgentType("");
    setMaxStep(0);
    setTokenStats(null);
    setChatLoading(false);
    chatLoadingRef.current = false;
  }

  React.useEffect(() => {
    if (!authenticated) return;
    // 切换项目时中止进行中的请求并重置会话
    chatAbortRef.current?.abort();
    if (sessionIdRef.current && sessionLoadedRef.current) {
      startNewChat();
    }
    queryClient.invalidateQueries({ queryKey: queryKeys.sessions.all(selectedProjectID || undefined) });
  }, [authenticated, selectedProjectID]);

  function selectServerFromUI(nodeletID: string) {
    if (!selectedProjectID) return;
    if (selectedNodeletID !== nodeletID) {
      setSelectedContainerID("");
    }
    setSelectedNodeletID(nodeletID);
  }

  function selectContainerFromUI(nodeletID: string, containerID: string) {
    if (!selectedProjectID) return;
    if (!expandedServers.set.has(nodeletID)) {
      expandedServers.add(nodeletID);
    }
    setSelectedNodeletID(nodeletID);
    setSelectedContainerID(containerID);
    setSelectedMCPConnectionID("");
  }

  function selectMCPConnection(conn: ProjectMCPConnection) {
    if (!selectedProjectID) return;
    setSelectedMCPConnectionID(conn.id);
    // 如果 MCP 连接绑定了容器，同步选中对应的 nodelet 和容器
    if (conn.nodeletId) setSelectedNodeletID(conn.nodeletId);
    setSelectedContainerID(conn.containerId || "");
  }

  function createMCPConnection(prefill: MCPPrefill) {
    setMCPPrefill(prefill);
    mcpForm.onOpen();
  }

  function editMCPConnection(conn: ProjectMCPConnection) {
    setMCPPrefill(null);
    mcpForm.onOpen(conn);
  }

  function toggleServer(nodeletID: string) {
    if (expandedServers.set.has(nodeletID)) {
      expandedServers.remove(nodeletID);
    } else {
      expandedServers.add(nodeletID);
      selectServerFromUI(nodeletID);
    }
  }

  // ---------------- Effect 级联：状态 → 数据加载 ----------------

  // Effect A: 项目进入/离开
  React.useEffect(() => {
    if (!authenticated) return;
    if (selectedProjectID && selectedProjectID !== lastLoadedProjectRef.current) {
      lastLoadedProjectRef.current = selectedProjectID;
      autoExpandFirstServerRef.current = false;
      setSelectedNodeletID("");
      setSelectedContainerID("");
      expandedServers.clear();
      // 日志流由 useLogStream hook 管理，containerId 变为空时会自动关闭
      // servers 由 useProjectServers 的 query key 变化自动重新 fetch
    }
    if (!selectedProjectID && lastLoadedProjectRef.current) {
      lastLoadedProjectRef.current = "";
      autoExpandFirstServerRef.current = false;
      setSelectedNodeletID("");
      setSelectedContainerID("");
      expandedServers.clear();
    }
  }, [authenticated, selectedProjectID]);

  // Effect C 已移除：日志流生命周期由 useLogStream hook 管理，自动跟随 selectedNodeletID/selectedContainerID 变化
  // Effect D 已移除：自动选第一个容器的逻辑移入 ServerTree 的 ServerContainers 组件，
  // 通过 useContainers 的 isSuccess 触发。

  // Effect E: 进入项目概览且无展开的 server → 自动展开首台服务器并选中
  React.useEffect(() => {
    if (!shouldAutoExpandFirstServer({
      authenticated,
      view: route.view,
      serverCount: servers.length,
      serversLoading,
      expandedCount: expandedServers.set.size,
      autoExpandConsumed: autoExpandFirstServerRef.current,
    })) return;

    const first = servers[0];
    autoExpandFirstServerRef.current = true;
    expandedServers.setState(new Set([first.nodelet.id]));
    setSelectedNodeletID(first.nodelet.id);
    setSelectedContainerID("");
  }, [authenticated, route.view, servers, serversLoading, expandedServers.set.size, selectedProjectID]);

  // ---------------- 基础 Effects ----------------
  // projects / skills 数据由 TanStack Query hooks 自动管理，无需手动 fetch

  React.useEffect(() => {
    return () => {
      chatAbortRef.current?.abort();
    };
  }, []);

  // ---- 标题 ----
  const selectedProject = projects.find((p) => p.id === selectedProjectID);
  const isProjectRoute = Boolean(selectedProjectID);

  React.useEffect(() => {
    const parts: string[] = [];
    if (selectedProject) parts.push(selectedProject.name);
    if (selectedProject && projectSection === "tools") parts.push("工具管理");
    if (selectedProject && projectSection === "skills") parts.push("技能管理");
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
  if (needsSetup) {
    return <SetupPage />;
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
        if (id === "servers") navigate({ view: "servers" });
        else if (id === "console") navigate({ view: "console" });
        else if (id === "chat") {
          if (selectedProjectID) navigate({ view: "project-chat", projectId: selectedProjectID });
          else navigate({ view: "projects" });
        }
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
                if (id === "servers") navigate({ view: "servers" });
        else if (id === "console") navigate({ view: "console" });
                else if (id === "projects") navigate({ view: "projects" });
              }
        }
      />

      <main id="main-content" className="content">
		{route.view === "servers" && (
		  <section className="workspace-card">
		    <NodeletManagementView />
		  </section>
		)}


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
              onRefresh={() => queryClient.invalidateQueries({ queryKey: queryKeys.projects.all })}
            />
          </section>
        )}

        {activeNav === "projects" && selectedProject && projectSection === "overview" && (
          <ProjectDetailView
            project={selectedProject}
            servers={servers}
            serversLoading={serversLoading}
            serverError={serverError}
            selectedNodeletID={selectedNodeletID}
            selectedContainerID={selectedContainerID}
            selectedMCPConnectionID={selectedMCPConnectionID}
            expandedServers={expandedServers.set}
            logs={logs}
            logsLoading={logsLoading}
            logsError={logsError}
            autoScroll={autoScroll}
            onBack={goToProjectList}
            onToggleServer={toggleServer}
            onSelectContainer={selectContainerFromUI}
            onSelectMCPConnection={selectMCPConnection}
            onCreateMCPConnection={createMCPConnection}
            onEditMCPConnection={editMCPConnection}
            onAutoScrollChange={setAutoScroll}
            onClearLogs={clearLogs}
            logsPanelRef={logsPanel}
            onMCPChanged={() => {
              queryClient.invalidateQueries({ queryKey: queryKeys.projects.all });
              queryClient.invalidateQueries({ queryKey: queryKeys.mcp.byProject(selectedProjectID) });
              if (selectedProjectID && selectedNodeletID && selectedContainerID) {
                queryClient.invalidateQueries({
                  queryKey: queryKeys.containerDetail.byId(selectedProjectID, selectedNodeletID, selectedContainerID),
                });
              }
            }}
          />
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

        {activeNav === "projects" && selectedProject && projectSection === "skills" && (
          <section className="workspace-card">
            <div className="workspace-head">
              <div>
                <h1>技能管理</h1>
                <p>管理 LLM Agent 的技能定义。技能是专业性工作流程指导，Agent 在需要时通过 skill 工具自主加载。</p>
              </div>
            </div>
            <SkillsView skills={skills} onRefresh={() => queryClient.invalidateQueries({ queryKey: queryKeys.skills.all })} />
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
              agentType={agentType}
              maxStep={maxStep}
              tokenStats={tokenStats}
              agentMeta={agentMeta}
              onInputChange={setChatInput}
              onSend={() => sendChat()}
              onClear={clearChat}
              onNewChat={startNewChat}
              onSelectSession={switchSession}
            />
          </section>
        )}

        {activeNav === "projects" && selectedProject && projectSection === "console" && (
          <section className="workspace-card">
            <ConsolePanel />
          </section>
        )}
      </main>

      {/* App 级 MCP 表单模态框 —— 供 workspace head 按钮、快捷卡片和编辑共用 */}
      {mcpForm.open && (
        <MCPFormModal
          editItem={mcpForm.data}
          prefill={!mcpForm.data ? mcpPrefill : null}
          containerOptions={mcpContainerOptions}
          containerOptionsLoading={mcpContainerOptionsLoading}
          onClose={() => { mcpForm.onClose(); setMCPPrefill(null); }}
          onSaved={(config) => {
            mcpForm.onClose();
            setMCPPrefill(null);
            setSelectedMCPConnectionID(config.id);
            if (config.nodeletId) setSelectedNodeletID(config.nodeletId);
            setSelectedContainerID(config.containerId || "");
            // Refresh project-scoped MCP list so overview picks up new/edited connections.
            queryClient.invalidateQueries({ queryKey: queryKeys.mcp.byProject(selectedProjectID) });
            queryClient.invalidateQueries({ queryKey: queryKeys.mcp.all });
            queryClient.invalidateQueries({ queryKey: queryKeys.tools.all });
          }}
        />
      )}
    </div>
  );
}
