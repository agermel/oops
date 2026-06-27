import React from "react";
import { flushSync } from "react-dom";
import debounce from "lodash.debounce";
import { RefreshCw, Search, ChevronDown } from "lucide-react";
import type { StatusItem, NodeletItem, Container, LogEntry, StepEvent, ChatExchange } from "./types";
import { MAX_LOGS, LOG_FLUSH_MS, LOG_MAX_WAIT_MS } from "./types";
import { Header } from "./components/Header";
import { SideRail } from "./components/SideRail";
import { AppBar } from "./components/AppBar";
import { ConnectionsView } from "./components/ConnectionsView";
import { FleetView } from "./components/FleetView";
import { ChatView } from "./components/ChatView";
import "./styles.css";

export function App() {
  const [activeNav, setActiveNav] = React.useState("connections");
  const [items, setItems] = React.useState<StatusItem[]>([]);
  const [nodelets, setNodelets] = React.useState<NodeletItem[]>([]);
  const [containers, setContainers] = React.useState<Container[]>([]);
  const [logs, setLogs] = React.useState<LogEntry[]>([]);
  const [selectedNodelet, setSelectedNodelet] = React.useState("");
  const [selectedContainer, setSelectedContainer] = React.useState("");
  const [loading, setLoading] = React.useState(true);
  const [nodeletsLoading, setNodeletsLoading] = React.useState(true);
  const [containersLoading, setContainersLoading] = React.useState(false);
  const [logsLoading, setLogsLoading] = React.useState(false);
  const [autoScroll, setAutoScroll] = React.useState(true);
  const [error, setError] = React.useState("");
  const [nodeletError, setNodeletError] = React.useState("");
  const [chatExchanges, setChatExchanges] = React.useState<ChatExchange[]>([]);
  const [currentSteps, setCurrentSteps] = React.useState<StepEvent[]>([]);
  const [chatInput, setChatInput] = React.useState("");
  const [currentQuestion, setCurrentQuestion] = React.useState("");
  const [chatLoading, setChatLoading] = React.useState(false);
  const [chatError, setChatError] = React.useState("");

  const logEventSource = React.useRef<EventSource | null>(null);
  const logBuffer = React.useRef<LogEntry[]>([]);
  const logsPanel = React.useRef<HTMLDivElement | null>(null);

  const flushLogs = React.useMemo(
    () =>
      debounce(
        () => {
          if (logBuffer.current.length === 0) {
            return;
          }
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

  const counters = React.useMemo(() => {
    return items.reduce(
      (acc, item) => {
        acc.total += 1;
        acc[item.result?.status ?? "unknown"] += 1;
        return acc;
      },
      { total: 0, alive: 0, dead: 0, unknown: 0 }
    );
  }, [items]);

  async function refreshConnections() {
    setLoading(true);
    setError("");
    try {
      const response = await fetch("/api/connections/status");
      if (!response.ok) {
        throw new Error(`HTTP ${response.status}`);
      }
      setItems(await response.json());
    } catch (err) {
      setError(err instanceof Error ? err.message : "连接状态读取失败");
    } finally {
      setLoading(false);
    }
  }

  async function refreshNodelets() {
    setNodeletsLoading(true);
    setNodeletError("");
    try {
      const response = await fetch("/api/nodelets");
      if (!response.ok) {
        throw new Error(`HTTP ${response.status}`);
      }
      const nextNodelets: NodeletItem[] = await response.json();
      setNodelets(nextNodelets);

      const nextSelected = selectedNodelet || nextNodelets[0]?.nodelet.id || "";
      setSelectedNodelet(nextSelected);
      if (nextSelected) {
        await loadContainers(nextSelected);
      }
    } catch (err) {
      setNodeletError(err instanceof Error ? err.message : "机器列表读取失败");
    } finally {
      setNodeletsLoading(false);
    }
  }

  async function loadContainers(nodeletId: string) {
    closeLogStream();
    setSelectedNodelet(nodeletId);
    setSelectedContainer("");
    setLogs([]);
    setContainersLoading(true);
    setNodeletError("");
    try {
      const response = await fetch(`/api/nodelets/${encodeURIComponent(nodeletId)}/containers`);
      if (!response.ok) {
        throw new Error(`HTTP ${response.status}`);
      }
      setContainers(await response.json());
    } catch (err) {
      setContainers([]);
      setNodeletError(err instanceof Error ? err.message : "容器列表读取失败");
    } finally {
      setContainersLoading(false);
    }
  }

  function loadLogs(nodeletId: string, containerId: string) {
    closeLogStream();
    setSelectedContainer(containerId);
    setLogsLoading(true);
    setLogs([]);
    setNodeletError("");

    const url = `/api/nodelets/${encodeURIComponent(nodeletId)}/containers/${encodeURIComponent(containerId)}/logs/stream?tail=100`;
    const source = new EventSource(url);
    logEventSource.current = source;

    source.onopen = () => {
      setLogsLoading(false);
      setNodeletError("");
    };
    source.onmessage = (event) => {
      try {
        logBuffer.current = [...logBuffer.current, JSON.parse(event.data) as LogEntry];
        flushLogs();
      } catch (err) {
        setNodeletError(err instanceof Error ? err.message : "日志事件解析失败");
      }
    };
    source.onerror = () => {
      setLogsLoading(false);
      setNodeletError("日志流连接失败");
    };
  }

  async function refresh() {
    await Promise.all([refreshConnections(), refreshNodelets()]);
  }

  async function sendChat(question?: string) {
    const q = (question ?? chatInput).trim();
    if (!q || chatLoading) {
      return;
    }
    setChatInput("");
    setChatError("");
    setCurrentSteps([]);
    setCurrentQuestion(q);
    setChatLoading(true);

    try {
      const response = await fetch("/api/chat", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ question: q }),
      });
      if (!response.ok) {
        const data = await response.json().catch(() => ({ error: `HTTP ${response.status}` }));
        throw new Error(data.error || `HTTP ${response.status}`);
      }

      // 读取 SSE 流。
      const reader = response.body!.getReader();
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
          if (line === "data: [DONE]") {
            streamDone = true;
            break;
          }
          if (line.startsWith("data: ")) {
            try {
              const evt: StepEvent = JSON.parse(line.slice(6));
              flushSync(() => {
                setCurrentSteps((prev) => [...prev, evt]);
              });
            } catch {
              // 跳过无法解析的行。
            }
          }
        }
      }

      // 将当前 exchange 归档。
      setCurrentSteps((steps) => {
        const answer = steps.find((s) => s.type === "answer");
        const errStep = steps.find((s) => s.type === "error");
        setChatExchanges((prev) => [...prev, { question: q, steps, answer: answer?.content, error: errStep?.content }]);
        return [];
      });
      setCurrentQuestion("");
      setChatLoading(false);
    } catch (err) {
      const msg = err instanceof Error ? err.message : "聊天请求失败";
      setChatError(msg);
      setCurrentQuestion("");
      setChatLoading(false);
    }
  }

  React.useEffect(() => {
    refresh();
  }, []);

  React.useEffect(() => {
    return () => {
      closeLogStream();
    };
  }, [flushLogs]);

  React.useEffect(() => {
    if (autoScroll && logsPanel.current) {
      logsPanel.current.scrollTop = logsPanel.current.scrollHeight;
    }
  }, [logs, autoScroll]);

  return (
    <div className="shell">
      <Header activeNav={activeNav} onNavChange={setActiveNav} />
      <SideRail activeNav={activeNav} onNavChange={setActiveNav} />
      <AppBar />

      <main className="content">
        <section className="workspace-card">
          <div className="workspace-head">
            <div>
              <h1>连接面板</h1>
              <p>集中查看组件可达性、机器列表、容器列表和容器日志。</p>
            </div>
            <button className="primary-button" onClick={refresh} disabled={loading}>
              <RefreshCw size={17} className={loading ? "spin" : ""} />
              <span>刷新状态</span>
            </button>
          </div>

          <div className="toolbar">
            <label className="local-search">
              <Search size={18} />
              <input placeholder="Search..." />
            </label>
            <button className="filter-button">
              <span>标签</span>
              <ChevronDown size={17} />
            </button>
          </div>

          {activeNav === "connections" && (
            <ConnectionsView
              items={items}
              loading={loading}
              error={error}
              counters={counters}
              onRefresh={refresh}
            />
          )}

          {activeNav === "fleet" && (
            <FleetView
              nodelets={nodelets}
              nodeletsLoading={nodeletsLoading}
              nodeletError={nodeletError}
              selectedNodelet={selectedNodelet}
              containers={containers}
              containersLoading={containersLoading}
              logs={logs}
              logsLoading={logsLoading}
              selectedContainer={selectedContainer}
              autoScroll={autoScroll}
              onSelectNodelet={loadContainers}
              onLoadLogs={loadLogs}
              onClearLogs={() => {
                flushLogs.cancel();
                logBuffer.current = [];
                setLogs([]);
              }}
              onAutoScrollChange={setAutoScroll}
              logsPanelRef={logsPanel}
            />
          )}

          {activeNav === "chat" && (
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
          )}
        </section>
      </main>
    </div>
  );
}
