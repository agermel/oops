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
import { Header } from "./components/Header";
import { SideRail } from "./components/SideRail";
import { ProjectsView } from "./components/ProjectsView";
import { ProjectDetailView } from "./components/ProjectDetailView";
import { ChatView } from "./components/ChatView";
import "./styles.css";

export function App() {
  const [activeNav, setActiveNav] = React.useState("projects");
  const [sidebarCollapsed, setSidebarCollapsed] = React.useState(false);

  // ---- 项目状态 ----
  const [projects, setProjects] = React.useState<Project[]>([]);
  const [projectsLoading, setProjectsLoading] = React.useState(false);
  const [projectsError, setProjectsError] = React.useState("");
  const [selectedProjectID, setSelectedProjectID] = React.useState("");

  // ---- 服务器 & 容器树状态 ----
  const [servers, setServers] = React.useState<ServerWithNodelet[]>([]);
  const [serversLoading, setServersLoading] = React.useState(false);
  const [serverError, setServerError] = React.useState("");
  const [containers, setContainers] = React.useState<Record<string, ContainerWithType[]>>({});
  const [containersLoading, setContainersLoading] = React.useState(false);
  const [selectedNodeletID, setSelectedNodeletID] = React.useState("");
  const [selectedContainerID, setSelectedContainerID] = React.useState("");
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

  // ---- 项目 CRUD ----
  async function fetchProjects() {
    setProjectsLoading(true);
    setProjectsError("");
    try {
      const resp = await fetch("/api/projects");
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      setProjects(await resp.json());
    } catch (err) {
      setProjectsError(err instanceof Error ? err.message : "读取项目列表失败");
    } finally {
      setProjectsLoading(false);
    }
  }

  function enterProject(id: string) {
    setSelectedProjectID(id);
    setSelectedContainerID("");
    setSelectedNodeletID("");
    setContainerDetail(undefined);
    setExpandedServers(new Set());
    setContainers({});
    closeLogStream();
    setLogs([]);
    loadProjectServers(id);
  }

  function leaveProject() {
    setSelectedProjectID("");
    setServers([]);
    setContainers({});
    setExpandedServers(new Set());
    closeLogStream();
    setLogs([]);
  }

  // ---- 服务器 ----
  async function loadProjectServers(projectID: string) {
    setServersLoading(true);
    setServerError("");
    try {
      const resp = await fetch(`/api/projects/${encodeURIComponent(projectID)}/servers`);
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const data: ServerWithNodelet[] = await resp.json();
      setServers(data);
      // 自动展开第一台服务器。
      if (data.length > 0) {
        setExpandedServers(new Set([data[0].nodelet.id]));
      }
    } catch (err) {
      setServerError(err instanceof Error ? err.message : "读取服务器列表失败");
    } finally {
      setServersLoading(false);
    }
  }

  async function toggleServer(nodeletID: string) {
    const next = new Set(expandedServers);
    if (next.has(nodeletID)) {
      next.delete(nodeletID);
      setExpandedServers(next);
    } else {
      next.add(nodeletID);
      setExpandedServers(next);
      // 按需加载容器列表。
      if (!containers[nodeletID]) {
        loadContainers(nodeletID);
      }
    }
  }

  async function loadContainers(nodeletID: string) {
    setContainersLoading(true);
    try {
      const pid = encodeURIComponent(selectedProjectID);
      const nid = encodeURIComponent(nodeletID);
      const resp = await fetch(`/api/projects/${pid}/servers/${nid}/containers`);
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const data: ContainerWithType[] = await resp.json();
      setContainers((prev) => ({ ...prev, [nodeletID]: data }));
    } catch (err) {
      setServerError(err instanceof Error ? err.message : "读取容器列表失败");
    } finally {
      setContainersLoading(false);
    }
  }

  async function selectContainer(nodeletID: string, containerID: string) {
    closeLogStream();
    setLogs([]);
    setSelectedNodeletID(nodeletID);
    setSelectedContainerID(containerID);
    setDetailError("");
    setHealth(undefined);

    // 加载容器详情。
    setDetailLoading(true);
    try {
      const pid = encodeURIComponent(selectedProjectID);
      const nid = encodeURIComponent(nodeletID);
      const cid = encodeURIComponent(containerID);
      const resp = await fetch(`/api/projects/${pid}/servers/${nid}/containers/${cid}`);
      if (!resp.ok) {
        const data = await resp.json().catch(() => ({ error: `HTTP ${resp.status}` }));
        throw new Error(data.error || `HTTP ${resp.status}`);
      }
      setContainerDetail(await resp.json());
    } catch (err) {
      setDetailError(err instanceof Error ? err.message : "读取容器详情失败");
      setContainerDetail(undefined);
    } finally {
      setDetailLoading(false);
    }

    // 启动日志流。
    loadLogStream(nodeletID, containerID);
  }

  function loadLogStream(nodeletID: string, containerID: string) {
    closeLogStream();
    setLogsLoading(true);
    setLogs([]);

    const pid = encodeURIComponent(selectedProjectID);
    const nid = encodeURIComponent(nodeletID);
    const cid = encodeURIComponent(containerID);
    const url = `/api/projects/${pid}/servers/${nid}/containers/${cid}/logs/stream?tail=100`;

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
        // 跳过无法解析的日志行。
      }
    };
    source.onerror = () => {
      setLogsLoading(false);
    };
  }

  // ---- 健康检查 ----
  async function checkHealth() {
    if (!selectedContainerID || !selectedNodeletID) return;
    setHealthLoading(true);
    try {
      const pid = encodeURIComponent(selectedProjectID);
      const nid = encodeURIComponent(selectedNodeletID);
      const cid = encodeURIComponent(selectedContainerID);
      const resp = await fetch(`/api/projects/${pid}/servers/${nid}/containers/${cid}/check`, { method: "POST" });
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      setHealth(await resp.json());
    } catch (err) {
      setHealth({ status: "unknown", message: err instanceof Error ? err.message : "探测失败", latency: 0 });
    } finally {
      setHealthLoading(false);
    }
  }

  // ---- 聊天 ----
  async function sendChat(question?: string) {
    const q = (question ?? chatInput).trim();
    if (!q || chatLoading) return;
    setChatInput("");
    setChatError("");
    setCurrentSteps([]);
    setCurrentQuestion(q);
    setChatLoading(true);

    try {
      const url = selectedProjectID
        ? `/api/projects/${encodeURIComponent(selectedProjectID)}/chat`
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
              setCurrentSteps((prev) => [...prev, evt]);
            } catch { /* skip */ }
          }
        }
      }

      setCurrentSteps((steps) => {
        const answer = steps.find((s) => s.type === "answer");
        const errStep = steps.find((s) => s.type === "error");
        setChatExchanges((prev) => [...prev, { question: q, steps, answer: answer?.content, error: errStep?.content }]);
        return [];
      });
      setCurrentQuestion("");
      setChatLoading(false);
    } catch (err) {
      setChatError(err instanceof Error ? err.message : "聊天请求失败");
      setCurrentQuestion("");
      setChatLoading(false);
    }
  }

  // ---- Effects ----
  React.useEffect(() => {
    fetchProjects();
  }, []);

  React.useEffect(() => {
    return () => closeLogStream();
  }, [flushLogs]);

  React.useEffect(() => {
    if (autoScroll && logsPanel.current) {
      logsPanel.current.scrollTop = logsPanel.current.scrollHeight;
    }
  }, [logs, autoScroll]);

  // ---- Render ----
  const selectedProject = projects.find((p) => p.id === selectedProjectID);

  return (
    <div className={`shell ${sidebarCollapsed ? "sidebar-collapsed" : ""}`}>
      <Header activeNav={activeNav} onNavChange={setActiveNav} />
      <SideRail
        activeNav={activeNav}
        collapsed={sidebarCollapsed}
        onCollapsedChange={setSidebarCollapsed}
        onNavChange={setActiveNav}
      />

      <main className={`content ${activeNav === "projects" && selectedProject ? "project-detail-content" : ""}`}>
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
              onSelect={enterProject}
              onRefresh={fetchProjects}
            />
          </section>
        )}

        {activeNav === "projects" && selectedProject && (
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
            autoScroll={autoScroll}
            onBack={leaveProject}
            onToggleServer={toggleServer}
            onSelectContainer={selectContainer}
            onServersChanged={() => loadProjectServers(selectedProjectID)}
            onHealthCheck={checkHealth}
            onAutoScrollChange={setAutoScroll}
            onClearLogs={() => {
              flushLogs.cancel();
              logBuffer.current = [];
              setLogs([]);
            }}
            logsPanelRef={logsPanel}
          />
        )}

        {activeNav === "chat" && (
          <section className="workspace-card">
            <ChatView
              chatExchanges={chatExchanges}
              currentSteps={currentSteps}
              currentQuestion={currentQuestion}
              chatInput={chatInput}
              chatLoading={chatLoading}
              chatError={chatError}
              onInputChange={setChatInput}
              onSend={() => sendChat()}
            />
          </section>
        )}
      </main>
    </div>
  );
}
