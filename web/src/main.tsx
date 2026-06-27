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

const navigation = [
  { label: "Connections", icon: Server, active: true },
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
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState("");

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

  async function refresh() {
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
              <p>集中查看 ES、Jaeger、Nacos、MySQL、Redis、Etcd、Kafka、OTel 的可达性。</p>
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
