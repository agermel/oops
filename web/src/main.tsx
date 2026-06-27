import React from "react";
import ReactDOM from "react-dom/client";
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
  XCircle
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

// AgentConfig 对应中心端配置的一台 agent。
type AgentConfig = {
  id: string;
  name: string;
  address: string;
};

// Host 对应 agent 返回的机器信息。
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

// AgentItem 是中心端机器列表接口的一行数据。
type AgentItem = {
  agent: AgentConfig;
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

function App() {
  const [items, setItems] = React.useState<StatusItem[]>([]);
  const [agents, setAgents] = React.useState<AgentItem[]>([]);
  const [containers, setContainers] = React.useState<Container[]>([]);
  const [logs, setLogs] = React.useState<LogEntry[]>([]);
  const [selectedAgent, setSelectedAgent] = React.useState("");
  const [selectedContainer, setSelectedContainer] = React.useState("");
  const [loading, setLoading] = React.useState(true);
  const [agentsLoading, setAgentsLoading] = React.useState(true);
  const [containersLoading, setContainersLoading] = React.useState(false);
  const [logsLoading, setLogsLoading] = React.useState(false);
  const [error, setError] = React.useState("");
  const [agentError, setAgentError] = React.useState("");

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

  async function refreshAgents() {
    setAgentsLoading(true);
    setAgentError("");
    try {
      const response = await fetch("/api/agents");
      if (!response.ok) {
        throw new Error(`HTTP ${response.status}`);
      }
      const nextAgents: AgentItem[] = await response.json();
      setAgents(nextAgents);

      const nextSelected = selectedAgent || nextAgents[0]?.agent.id || "";
      setSelectedAgent(nextSelected);
      if (nextSelected) {
        await loadContainers(nextSelected);
      }
    } catch (err) {
      setAgentError(err instanceof Error ? err.message : "机器列表读取失败");
    } finally {
      setAgentsLoading(false);
    }
  }

  async function loadContainers(agentId: string) {
    setSelectedAgent(agentId);
    setSelectedContainer("");
    setLogs([]);
    setContainersLoading(true);
    setAgentError("");
    try {
      const response = await fetch(`/api/agents/${encodeURIComponent(agentId)}/containers`);
      if (!response.ok) {
        throw new Error(`HTTP ${response.status}`);
      }
      setContainers(await response.json());
    } catch (err) {
      setContainers([]);
      setAgentError(err instanceof Error ? err.message : "容器列表读取失败");
    } finally {
      setContainersLoading(false);
    }
  }

  async function loadLogs(agentId: string, containerId: string) {
    setSelectedContainer(containerId);
    setLogsLoading(true);
    setAgentError("");
    try {
      const response = await fetch(
        `/api/agents/${encodeURIComponent(agentId)}/containers/${encodeURIComponent(containerId)}/logs?tail=100`
      );
      if (!response.ok) {
        throw new Error(`HTTP ${response.status}`);
      }
      setLogs(await response.json());
    } catch (err) {
      setLogs([]);
      setAgentError(err instanceof Error ? err.message : "日志读取失败");
    } finally {
      setLogsLoading(false);
    }
  }

  async function refresh() {
    await Promise.all([refreshConnections(), refreshAgents()]);
  }

  React.useEffect(() => {
    refresh();
  }, []);

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
          <button title="智能助手">
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
            {agentError && <div className="error-line">{agentError}</div>}
            <div className="agent-grid">
              {agentsLoading && agents.length === 0 ? (
                <div className="empty-card">正在读取机器列表</div>
              ) : (
                agents.map((item) => (
                  <button
                    key={item.agent.id}
                    className={`agent-card ${selectedAgent === item.agent.id ? "selected" : ""}`}
                    onClick={() => loadContainers(item.agent.id)}
                  >
                    <span className={`agent-dot ${item.available ? "alive" : "dead"}`} />
                    <span>
                      <strong>{item.host.name || item.agent.name || item.agent.id}</strong>
                      <small>{item.agent.address}</small>
                    </span>
                    <span className="agent-meta">
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
                          <button title="查看日志" onClick={() => loadLogs(selectedAgent, container.id)}>
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
            <div className="section-title">
              <FileText size={18} />
              <h2>日志列表</h2>
            </div>
            <div className="logs-panel">
              {logsLoading ? (
                <div className="empty-card">正在读取日志</div>
              ) : logs.length === 0 ? (
                <div className="empty-card">选择一个容器查看最近 100 行日志</div>
              ) : (
                logs.map((entry, index) => (
                  <div key={`${entry.timestamp}-${index}`} className="log-line">
                    <span>{entry.timestamp || "-"}</span>
                    <b>{entry.stream}</b>
                    <code>{entry.message}</code>
                  </div>
                ))
              )}
            </div>
          </section>
        </section>
      </main>
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
