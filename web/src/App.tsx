import React from "react";
import { Plus } from "lucide-react";
import { Button } from "./components/ui/Button";
import type {
  ContainerWithType,
  ContainerDetail as ContainerDetailType,
  HealthResult,
  LogEntry,
  StepEvent,
  ChatExchange,
  SessionDetail,
  MCPConnectionStatus,
} from "./types";
import { MAX_LOGS, LOG_FLUSH_MS, LOG_MAX_WAIT_MS } from "./types";
import { useQueryClient } from "@tanstack/react-query";
import { usePathRouter } from "./hooks/usePathRouter";
import { useProjects } from "./hooks/useProjects";
import { useProjectServers, useNodeletStatus } from "./hooks/useServers";
import { useSkills } from "./hooks/useSkills";
import { useSessions } from "./hooks/useSessions";
import { queryKeys } from "./hooks/queries";
import { pageConfig } from "./lib/config";
import { apiRequest, getErrorMessage } from "./lib/api";
import { projectPaths, serverPaths, sessionPaths } from "./lib/paths";
import { shouldAutoExpandFirstServer } from "./lib/serverTreeState";
import { Header } from "./components/Header";
import { SideRail } from "./components/SideRail";
import { ProjectsView } from "./components/ProjectsView";
import { ProjectDetailView } from "./components/ProjectDetailView";
import { NodeletManagementView } from "./components/NodeletManagementView";
import { ChatView } from "./components/ChatView";
import { MCPView } from "./components/MCPView";
import { MCPFormModal } from "./components/MCPFormModal";
import { ToolsView } from "./components/ToolsView";
import { SkillsView } from "./components/SkillsView";
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
  const routeViewRef = React.useRef(route.view);
  routeViewRef.current = route.view;

  const activeNav = route.view === "servers" ? "servers" : route.view === "console" ? "console" : "projects";
  const selectedProjectID =
    route.view === "projects" || route.view === "servers" || route.view === "console"
      ? ""
      : route.projectId || "";
  const projectSection: string =
    route.view === "project-mcp" ? "mcp" :
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
  const [mcpFormOpen, setMCPFormOpen] = React.useState(false);
  const [mcpQuickType, setMCPQuickType] = React.useState<string | undefined>(undefined);
  const [mcpEditItem, setMCPEditItem] = React.useState<MCPConnectionStatus | null>(null);
  const [mcpViewKey, setMCPViewKey] = React.useState(0);

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
  } = useSessions(selectedProjectID_clean || undefined);

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
        error: si.error || sw.error,
      };
    });
  }, [rawServers, nodeletStatusItems]);

  // ---- 服务器 & 容器树状态 ----
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
  const sessionIdRef = React.useRef(sessionId);
  sessionIdRef.current = sessionId;
  const [sessionLoaded, setSessionLoaded] = React.useState(false);
  const sessionLoadedRef = React.useRef(sessionLoaded);
  sessionLoadedRef.current = sessionLoaded;
  const [agentType, setAgentType] = React.useState<string>("");
  const [maxStep, setMaxStep] = React.useState<number>(0);
  const [tokenStats, setTokenStats] = React.useState<{ tokens: number; trimmed: number } | null>(null);

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
            const answerContents = currentSteps.filter((s) => s.type === "answer").map((s) => s.content);
            exchanges.push({
              question: currentQuestion,
              steps: currentSteps,
              answer: answerContents.length > 0 ? answerContents.join("") : undefined,
            });
          }
          setChatExchanges(exchanges);
          setSessionLoaded(true);
          queryClient.invalidateQueries({ queryKey: queryKeys.sessions.all(selectedProjectID || undefined) });
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

  // Prober 状态通过 useNodeletStatus() 的 refetchInterval: 30_000 自动轮询，
  // servers 的合并通过 useMemo 完成（见上方），无需额外的 effect。

  async function loadContainers(projectID: string, nodeletID: string) {
    setContainersLoading((prev) => new Set(prev).add(nodeletID));
    try {
      const data = await apiRequest<ContainerWithType[]>(serverPaths(projectID, nodeletID).containers);
      setContainers((prev) => ({ ...prev, [nodeletID]: data }));
    } catch (err) {
      // 触发后端即时探测，让 Prober 感知失败；server 状态由 useMemo + Prober 数据驱动
      apiRequest(`/api/nodelets/${encodeURIComponent(nodeletID)}/probe`, { method: "POST" }).catch(() => {});
      setContainers((prev) => ({ ...prev, [nodeletID]: [] }));
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
      setLogsError(""); // 重连成功时清除之前的错误
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
      // EventSource 会自动重连，仅在首次连接失败或彻底断开时展示错误；
      // 重连成功后 onopen 会清除此错误。
      if (source.readyState === EventSource.CLOSED) {
        setLogsError("日志流连接失败，请检查容器是否在运行");
      }
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
    if (!q) return;
    // 自愈：若 UI 已不显示 loading 但 ref 泄漏（如导航中途离开聊天页），则重置
    if (!chatLoading) chatLoadingRef.current = false;
    if (chatLoadingRef.current) return;
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
          if (line.startsWith("data: [DONE]")) { streamDone = true; break; }
          if (line.startsWith("data: ")) {
            try {
              const evt: StepEvent = JSON.parse(line.slice(6));
              if (evt.type === "session" && evt.content) {
                setSessionId(evt.content);
                setSessionLoaded(true);
                localStorage.setItem("oops_session_id", evt.content);
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
    if (section === "mcp") navigate({ view: "project-mcp", projectId: selectedProjectID });
    else if (section === "chat") navigate({ view: "project-chat", projectId: selectedProjectID });
    else if (section === "console") navigate({ view: "project-console", projectId: selectedProjectID });
    else if (section === "tools") navigate({ view: "project-tools", projectId: selectedProjectID });
    else if (section === "skills") navigate({ view: "project-skills", projectId: selectedProjectID });
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
          const answerContents = currentSteps.filter((s) => s.type === "answer").map((s) => s.content);
          exchanges.push({
            question: currentQuestion,
            steps: currentSteps,
            answer: answerContents.length > 0 ? answerContents.join("") : undefined,
          });
        }
        setChatExchanges(exchanges);
        setCurrentSteps([]);
        chatStepsRef.current = [];
        setCurrentQuestion("");
        setChatError("");
        setSessionId(id);
        localStorage.setItem("oops_session_id", id);
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

  const queryClient = useQueryClient();

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
    queryClient.invalidateQueries({ queryKey: queryKeys.sessions.all(selectedProjectID || undefined) });
    setAgentType("");
    setMaxStep(0);
    setTokenStats(null);
    setChatLoading(false);
    chatLoadingRef.current = false;
  }

  React.useEffect(() => {
    if (!authenticated) return;
    // 切换项目时仅当用户在聊天页面才重置会话（通过 ref 读取最新值避免过期闭包）
    if (sessionIdRef.current && sessionLoadedRef.current && routeViewRef.current === "project-chat") {
      startNewChat();
    }
    queryClient.invalidateQueries({ queryKey: queryKeys.sessions.all(selectedProjectID || undefined) });
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
      // 展开并选中：触发即时探测获取最新连通状态
      apiRequest(`/api/nodelets/${encodeURIComponent(nodeletID)}/probe`, { method: "POST" }).catch(() => {});
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
      autoExpandFirstServerRef.current = false;
      setSelectedNodeletID("");
      setSelectedContainerID("");
      setContainers({});
      setExpandedServers(new Set());
      setContainerDetail(undefined);
      closeLogStream();
      setLogs([]);
      // servers 由 useProjectServers 的 query key 变化自动重新 fetch
    }
    if (!selectedProjectID && lastLoadedProjectRef.current) {
      lastLoadedProjectRef.current = "";
      autoExpandFirstServerRef.current = false;
      setSelectedNodeletID("");
      setSelectedContainerID("");
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
    if (containerDetail
      && containerDetail.container.id === selectedContainerID
      && containerDetail.container.hostId === selectedNodeletID) return;
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
    if (!shouldAutoExpandFirstServer({
      authenticated,
      view: route.view,
      serverCount: servers.length,
      serversLoading,
      expandedCount: expandedServers.size,
      autoExpandConsumed: autoExpandFirstServerRef.current,
    })) return;

    const first = servers[0];
    autoExpandFirstServerRef.current = true;
    setExpandedServers(new Set([first.nodelet.id]));
    setSelectedNodeletID(first.nodelet.id);
    setSelectedContainerID("");
    if (!containers[first.nodelet.id]) {
      loadContainers(selectedProjectID, first.nodelet.id);
    }
  }, [authenticated, route.view, servers, serversLoading, expandedServers.size, selectedProjectID, containers]);

  // ---------------- 基础 Effects ----------------
  // projects / skills 数据由 TanStack Query hooks 自动管理，无需手动 fetch

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
            onHealthCheck={checkHealth}
            onAutoScrollChange={setAutoScroll}
            onClearLogs={() => {
              flushLogs.cancel();
              logBuffer.current = [];
              setLogs([]);
            }}
            logsPanelRef={logsPanel}
            onMCPChanged={() => {
              queryClient.invalidateQueries({ queryKey: queryKeys.projects.all });
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
              <div className="workspace-head-actions">
                <Button size="sm" onClick={() => { setMCPQuickType(undefined); setMCPEditItem(null); setMCPFormOpen(true); }}>
                  <Plus size={15} />
                  <span>新建连接</span>
                </Button>
              </div>
            </div>
            <MCPView
              key={mcpViewKey}
              onQuickCreate={(type) => { setMCPQuickType(type); setMCPEditItem(null); setMCPFormOpen(true); }}
              onEdit={(item) => { setMCPEditItem(item); setMCPFormOpen(true); }}
            />
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
      {mcpFormOpen && (
        <MCPFormModal
          editItem={mcpEditItem}
          prefill={!mcpEditItem && mcpQuickType ? {
            name: "",
            type: mcpQuickType,
            env: [],
          } : null}
          onClose={() => { setMCPFormOpen(false); setMCPQuickType(undefined); setMCPEditItem(null); }}
          onSaved={() => {
            setMCPFormOpen(false);
            setMCPQuickType(undefined);
            setMCPEditItem(null);
            setMCPViewKey((k) => k + 1);
          }}
        />
      )}
    </div>
  );
}
