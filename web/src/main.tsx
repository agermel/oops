import React from "react";
import ReactDOM from "react-dom/client";
import AnsiConvertor from "ansi-to-html";
import debounce from "lodash.debounce";
import {
  AlertTriangle,
  Bell,
  CheckCircle2,
  ChevronDown,
  CircleHelp,
  Database,
  Edit3,
  FileText,
  Gauge,
  Menu,
  RefreshCw,
  Search,
  Server,
  Settings,
  Sparkles,
  TerminalSquare,
  XCircle,
  Send,
  Bot,
  User,
  X
} from "lucide-react";
import "./styles.css";

// Connection 对应后端返回的连接配置。
type Connection = {
  id: string;
  name: string;
  type: string;
  address: string;
};

// Result 对应后端一次健康探测的结果。
type Result = {
  connectionId: string;
  status: "alive" | "dead" | "unknown";
  message?: string;
  latency: number;
  checkedAt: string;
};

// StatusItem 是连接状态接口的一行数据。
type StatusItem = {
  connection: Connection;
  result: Result;
  error?: string;
};

// NodeletConfig 对应中心端配置的一台 nodelet。
type NodeletConfig = {
  id: string;
  name: string;
  address: string;
};

// Host 对应 nodelet 返回的机器信息。
type Host = {
  id: string;
  name: string;
  address: string;
  available: boolean;
  dockerVersion: string;
  runtime: string;
  nCPU: number;
  memTotal: number;
};

// NodeletItem 是中心端机器列表接口的一行数据。
type NodeletItem = {
  nodelet: NodeletConfig;
  host: Host;
  available: boolean;
  error?: string;
};

// Container 对应某台机器上的一个容器。
type Container = {
  id: string;
  name: string;
  image: string;
  state: string;
  health?: string;
  hostId: string;
  created: string;
  startedAt: string;
};

// LogEntry 对应一条容器日志。
type LogEntry = {
  timestamp: string;
  containerId: string;
  stream: string;
  message: string;
  rawMessage?: string;
  level?: "fatal" | "error" | "warn" | "info" | "debug" | "trace" | "unknown";
};

const navigation = [
  { label: "Ops Plane", icon: Server, active: true },
  { label: "Incidents", icon: AlertTriangle, active: false },
  { label: "Sources", icon: Database, active: false },
  { label: "Settings", icon: Settings, active: false }
];

const statusIcon = {
  alive: CheckCircle2,
  dead: XCircle,
  unknown: AlertTriangle
};

const MAX_LOGS = 2000;
const LOG_FLUSH_MS = 250;
const LOG_MAX_WAIT_MS = 1000;
const ansiConvertor = new AnsiConvertor({
  escapeXML: true,
  fg: "#f5f7fa",
  bg: "#1f2430"
});

function App() {
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
  const [chatOpen, setChatOpen] = React.useState(false);
  const [chatMessages, setChatMessages] = React.useState<{ role: string; content: string }[]>([]);
  const [chatInput, setChatInput] = React.useState("");
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
    setChatMessages((prev) => [...prev, { role: "user", content: q }]);
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
      const data = await response.json();
      setChatMessages((prev) => [...prev, { role: "assistant", content: data.answer }]);
    } catch (err) {
      const msg = err instanceof Error ? err.message : "聊天请求失败";
      setChatError(msg);
    } finally {
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
      <header className="global-header">
        <div className="elastic-brand" aria-label="Elastic">
          <span className="elastic-flower" aria-hidden="true">
            <i />
            <i />
            <i />
            <i />
          </span>
          <span>elastic</span>
        </div>
        <label className="global-search">
          <Search size={18} />
          <input placeholder="搜索 Ops Plane" />
        </label>
        <div className="global-actions">
          <button title="帮助">
            <CircleHelp size={20} />
          </button>
          <button title="通知">
            <Bell size={20} />
          </button>
          <button title="智能助手" onClick={() => setChatOpen((v) => !v)} className={chatOpen ? "chat-active" : ""}>
            <Sparkles size={20} />
          </button>
          <button className="avatar" title="用户">
            o
          </button>
        </div>
      </header>

      <aside className="side-rail">
        <button className="rail-menu" title="菜单">
          <Menu size={23} />
        </button>
        <nav className="rail-nav" aria-label="主导航">
          {navigation.map((item) => (
            <button key={item.label} className={item.active ? "active" : ""} title={item.label}>
              <item.icon size={19} />
            </button>
          ))}
        </nav>
      </aside>

      <section className="app-bar">
        <button className="space-badge">默</button>
        <div className="app-title">连接面板</div>
      </section>

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

          <section className="metrics" aria-label="连接状态统计">
            <Metric label="TOTAL" value={counters.total} tone="neutral" />
            <Metric label="ALIVE" value={counters.alive} tone="alive" />
            <Metric label="DEAD" value={counters.dead} tone="dead" />
            <Metric label="UNKNOWN" value={counters.unknown} tone="unknown" />
          </section>

          <section className="panel">
            <div>
              <div className="panel-summary">
                <Gauge size={16} />
                <span>{loading ? "探测中" : `${items.length} 个连接`}</span>
              </div>
            </div>

            {error && <div className="error-line">{error}</div>}

            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th className="check-cell">
                      <span className="checkbox" />
                    </th>
                    <th>标题</th>
                    <th>类型</th>
                    <th>地址</th>
                    <th>状态</th>
                    <th>延迟</th>
                    <th>诊断</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {loading && items.length === 0 ? (
                    <tr>
                      <td colSpan={8} className="empty">
                        <TerminalSquare size={16} />
                        <span>正在读取连接状态</span>
                      </td>
                    </tr>
                  ) : (
                    items.map((item) => <ConnectionRow key={item.connection.id} item={item} />)
                  )}
                </tbody>
              </table>
            </div>
          </section>

          <section className="fleet-section">
            <div className="section-title">
              <Server size={18} />
              <h2>机器列表</h2>
            </div>
            {nodeletError && <div className="error-line">{nodeletError}</div>}
            <div className="nodelet-grid">
              {nodeletsLoading && nodelets.length === 0 ? (
                <div className="empty-card">正在读取机器列表</div>
              ) : (
                nodelets.map((item) => (
                  <button
                    key={item.nodelet.id}
                    className={`nodelet-card ${selectedNodelet === item.nodelet.id ? "selected" : ""}`}
                    onClick={() => loadContainers(item.nodelet.id)}
                  >
                    <span className={`nodelet-dot ${item.available ? "alive" : "dead"}`} />
                    <span>
                      <strong>{item.host.name || item.nodelet.name || item.nodelet.id}</strong>
                      <small>{item.nodelet.address}</small>
                    </span>
                    <span className="nodelet-meta">
                      {item.available ? `${item.host.runtime || "docker"} ${item.host.dockerVersion || ""}` : "unavailable"}
                    </span>
                  </button>
                ))
              )}
            </div>
          </section>

          <section className="fleet-section">
            <div className="section-title">
              <TerminalSquare size={18} />
              <h2>容器列表</h2>
            </div>
            <div className="table-wrap compact">
              <table>
                <thead>
                  <tr>
                    <th>容器</th>
                    <th>镜像</th>
                    <th>状态</th>
                    <th>健康</th>
                    <th>主机</th>
                    <th>日志</th>
                  </tr>
                </thead>
                <tbody>
                  {containersLoading ? (
                    <tr>
                      <td colSpan={6} className="empty">
                        正在读取容器列表
                      </td>
                    </tr>
                  ) : containers.length === 0 ? (
                    <tr>
                      <td colSpan={6} className="empty">
                        暂无容器
                      </td>
                    </tr>
                  ) : (
                    containers.map((container) => (
                      <tr key={container.id} className={selectedContainer === container.id ? "selected-row" : ""}>
                        <td>
                          <div className="service-name">{container.name || container.id.slice(0, 12)}</div>
                          <div className="service-id">{container.id.slice(0, 12)}</div>
                        </td>
                        <td className="address">{container.image}</td>
                        <td>
                          <span className={`status ${container.state === "running" ? "alive" : "unknown"}`}>
                            {container.state}
                          </span>
                        </td>
                        <td>{container.health || "-"}</td>
                        <td className="message">{container.hostId}</td>
                        <td className="action-cell">
                          <button title="查看日志" onClick={() => loadLogs(selectedNodelet, container.id)}>
                            <FileText size={18} />
                          </button>
                        </td>
                      </tr>
                    ))
                  )}
                </tbody>
              </table>
            </div>
          </section>

          <section className="fleet-section">
            <div className="section-title logs-title">
              <span>
                <FileText size={18} />
                <h2>日志列表</h2>
              </span>
              <div className="log-actions">
                <label>
                  <input type="checkbox" checked={autoScroll} onChange={(event) => setAutoScroll(event.target.checked)} />
                  <span>自动滚动</span>
                </label>
                <button
                  type="button"
                  onClick={() => {
                    flushLogs.cancel();
                    logBuffer.current = [];
                    setLogs([]);
                  }}
                  disabled={logs.length === 0}
                >
                  清空
                </button>
              </div>
            </div>
            <div className="logs-panel" ref={logsPanel}>
              {logsLoading ? (
                <div className="empty-card">正在连接日志流</div>
              ) : logs.length === 0 ? (
                <div className="empty-card">{selectedContainer ? "等待实时日志" : "选择一个容器查看最近 100 行日志"}</div>
              ) : (
                logs.map((entry, index) => <LogRow key={`${entry.timestamp}-${index}`} entry={entry} />)
              )}
            </div>
          </section>

          {chatOpen && (
            <section className="chat-panel">
              <div className="chat-header">
                <span><Sparkles size={18} /> 智能助手</span>
                <button onClick={() => setChatOpen(false)} title="关闭">
                  <X size={18} />
                </button>
              </div>
              <div className="chat-body">
                {chatMessages.length === 0 && !chatLoading && (
                  <div className="chat-empty">问我任何关于当前环境的问题，例如"哪些容器在运行？"或"Redis 是否正常？"</div>
                )}
                {chatMessages.map((msg, i) => (
                  <div key={i} className={`chat-msg ${msg.role}`}>
                    <div className="chat-avatar">
                      {msg.role === "user" ? <User size={16} /> : <Bot size={16} />}
                    </div>
                    <div className="chat-content">{msg.content}</div>
                  </div>
                ))}
                {chatLoading && (
                  <div className="chat-msg assistant">
                    <div className="chat-avatar"><Bot size={16} /></div>
                    <div className="chat-content chat-typing">思考中<span>.</span><span>.</span><span>.</span></div>
                  </div>
                )}
                {chatError && <div className="chat-error">{chatError}</div>}
              </div>
              <div className="chat-footer">
                <input
                  placeholder="输入问题，按 Enter 发送"
                  value={chatInput}
                  onChange={(e) => setChatInput(e.target.value)}
                  onKeyDown={(e) => { if (e.key === "Enter") { sendChat(); } }}
                  disabled={chatLoading}
                />
                <button onClick={() => sendChat()} disabled={chatLoading || !chatInput.trim()} title="发送">
                  <Send size={18} />
                </button>
              </div>
            </section>
          )}
        </section>
      </main>
    </div>
  );
}

function LogRow({ entry }: { entry: LogEntry }) {
  const level = entry.level || "unknown";
  const html = ansiConvertor.toHtml(entry.rawMessage || entry.message || "");

  return (
    <div className={`log-line level-${level}`}>
      <span>{entry.timestamp || "-"}</span>
      <b className={`log-stream-tag ${entry.stream === "stderr" ? "stderr" : "stdout"}`}>{entry.stream}</b>
      <strong className={`log-level ${level}`}>{level}</strong>
      <code dangerouslySetInnerHTML={{ __html: html }} />
    </div>
  );
}

function Metric({ label, value, tone }: { label: string; value: number; tone: "neutral" | "alive" | "dead" | "unknown" }) {
  return (
    <div className={`metric ${tone}`}>
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

function ConnectionRow({ item }: { item: StatusItem }) {
  const status = item.result?.status ?? "unknown";
  const Icon = statusIcon[status];

  return (
    <tr>
      <td className="check-cell">
        <span className="checkbox" />
      </td>
      <td>
        <div className="service-name">{item.connection.name || item.connection.id}</div>
        <div className="service-id">{item.connection.id}</div>
      </td>
      <td>
        <span className="type-pill">{item.connection.type}</span>
      </td>
      <td className="address">{item.connection.address}</td>
      <td>
        <span className={`status ${status}`}>
          <Icon size={15} />
          {status}
        </span>
      </td>
      <td>{item.result?.latency ?? 0}ms</td>
      <td className="message">{item.error || item.result?.message || "-"}</td>
      <td className="action-cell">
        <button title="编辑连接">
          <Edit3 size={18} />
        </button>
      </td>
    </tr>
  );
}

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>
);
