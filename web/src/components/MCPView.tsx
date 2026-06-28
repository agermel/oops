import React from "react";
import { Plus, Trash2, Edit3, Wrench, X } from "lucide-react";
import type { MCPConnectionConfig, MCPConnectionStatus, MCPPrefill } from "../types";

// ---- 类型默认值 ----
const typeDefaults: Record<string, { command: string; args: string[]; env: string[] }> = {
  mysql: {
    command: "./mcp-servers/mysql/mysql-mcp-server",
    args: ["--silent"],
    env: ["MYSQL_DSN=user:pass@tcp(host:3306)/db?charset=utf8mb4"],
  },
  redis: {
    command: "./mcp-servers/redis/redis-mcp-server",
    args: [],
    env: ["REDIS_HOST=127.0.0.1", "REDIS_PORT=6379", "REDIS_DB=0", "REDIS_PWD="],
  },
  etcd: {
    command: "./mcp-servers/etcd/etcd-mcp-server",
    args: [],
    env: ["ETCD_ENDPOINTS=127.0.0.1:2379"],
  },
  postgres: {
    command: "uvx",
    args: ["--from", "mcp-server-postgres@latest", "mcp-server-postgres"],
    env: ["DATABASE_URL=postgres://user:pass@host:5432/db"],
  },
  other: {
    command: "",
    args: [],
    env: [],
  },
};

// 哪些类型显示连接参数字段
const typesWithCredentials = new Set(["mysql", "redis", "postgres", "etcd"]);

function emptyForm(type?: string): MCPConnectionStatus {
  const t = type || "mysql";
  const defaults = typeDefaults[t] || typeDefaults.other;
  return {
    id: "",
    name: "",
    type: t,
    command: defaults.command,
    args: [...defaults.args],
    env: [...defaults.env],
    enabled: true,
    status: "stopped",
    toolCount: 0,
  };
}

function formToConfig(form: MCPConnectionStatus): MCPConnectionConfig {
  return {
    id: form.id,
    name: form.name,
    type: form.type,
    command: form.command,
    args: form.args,
    env: form.env,
    enabled: form.enabled,
    containerId: form.containerId,
    nodeletId: form.nodeletId,
  };
}

// ---- 连接参数 ----
type Credentials = {
  host: string;
  port: string;
  user: string;
  password: string;
  database: string;
};

function emptyCreds(): Credentials {
  return { host: "", port: "", user: "", password: "", database: "" };
}

// 从环境变量列表反解连接参数
function parseCredentials(type: string, env: string[]): Credentials {
  const creds = emptyCreds();
  if (type === "mysql") {
    const dsn = env.find((e) => e.startsWith("MYSQL_DSN="))?.slice("MYSQL_DSN=".length) || "";
    const m = dsn.match(/^([^:]*):([^@]*)@tcp\(([^:]*):(\d*)\)\/(.*?)(\?.*)?$/);
    if (m) {
      creds.user = m[1] || "";
      creds.password = m[2] || "";
      creds.host = m[3] || "";
      creds.port = m[4] || "";
      creds.database = m[5] || "";
    }
  } else if (type === "redis") {
    creds.host = env.find((e) => e.startsWith("REDIS_HOST="))?.slice("REDIS_HOST=".length) || "";
    creds.port = env.find((e) => e.startsWith("REDIS_PORT="))?.slice("REDIS_PORT=".length) || "";
    creds.password = env.find((e) => e.startsWith("REDIS_PWD="))?.slice("REDIS_PWD=".length) || "";
    creds.database = env.find((e) => e.startsWith("REDIS_DB="))?.slice("REDIS_DB=".length) || "";
  } else if (type === "postgres") {
    const dsn = env.find((e) => e.startsWith("DATABASE_URL="))?.slice("DATABASE_URL=".length) || "";
    const m = dsn.match(/^postgres:\/\/([^:]*):([^@]*)@([^:]*):(\d*)\/(.*)$/);
    if (m) {
      creds.user = m[1] || "";
      creds.password = m[2] || "";
      creds.host = m[3] || "";
      creds.port = m[4] || "";
      creds.database = m[5] || "";
    }
  } else if (type === "etcd") {
    creds.host = env.find((e) => e.startsWith("ETCD_ENDPOINTS="))?.slice("ETCD_ENDPOINTS=".length) || "";
    creds.user = env.find((e) => e.startsWith("ETCD_USERNAME="))?.slice("ETCD_USERNAME=".length) || "";
    creds.password = env.find((e) => e.startsWith("ETCD_PASSWORD="))?.slice("ETCD_PASSWORD=".length) || "";
  }
  return creds;
}

// 从连接参数生成环境变量
function credentialsToEnv(type: string, creds: Credentials): string[] {
  if (type === "mysql") {
    const pass = creds.password ? `:${creds.password}` : "";
    const dsn = `${creds.user}${pass}@tcp(${creds.host}:${creds.port})/${creds.database}?charset=utf8mb4`;
    if (!creds.user && !creds.host) return []; // 没有实质性内容，不覆盖
    return [`MYSQL_DSN=${dsn}`];
  }
  if (type === "redis") {
    const env: string[] = [];
    if (creds.host) env.push(`REDIS_HOST=${creds.host}`);
    if (creds.port) env.push(`REDIS_PORT=${creds.port}`);
    if (creds.database) env.push(`REDIS_DB=${creds.database}`);
    if (creds.password) env.push(`REDIS_PWD=${creds.password}`);
    else env.push("REDIS_PWD=");
    return env;
  }
  if (type === "postgres") {
    const pass = creds.password ? `:${creds.password}` : "";
    const dsn = `postgres://${creds.user}${pass}@${creds.host}:${creds.port}/${creds.database}`;
    if (!creds.user && !creds.host) return [];
    return [`DATABASE_URL=${dsn}`];
  }
  if (type === "etcd") {
    const env: string[] = [];
    if (creds.host) env.push(`ETCD_ENDPOINTS=${creds.host}`);
    if (creds.user) env.push(`ETCD_USERNAME=${creds.user}`);
    if (creds.password) env.push(`ETCD_PASSWORD=${creds.password}`);
    return env;
  }
  return [];
}

// ---- MCPView ----
export function MCPView({
  prefill,
  onPrefillConsumed,
  onGoBack,
}: {
  prefill?: MCPPrefill | null;
  onPrefillConsumed?: () => void;
  onGoBack?: () => void;
}) {
  const [connections, setConnections] = React.useState<MCPConnectionStatus[]>([]);
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState("");

  // 表单状态
  const [showForm, setShowForm] = React.useState(false);
  const [editing, setEditing] = React.useState<MCPConnectionStatus | null>(null);
  const [isNew, setIsNew] = React.useState(true);
  const [saving, setSaving] = React.useState(false);
  const [saveError, setSaveError] = React.useState("");
  const [testResult, setTestResult] = React.useState("");
  const [testing, setTesting] = React.useState(false);
  const [toggling, setToggling] = React.useState<Set<string>>(new Set());

  // 跟踪当前表单是否由"一键配置 MCP"打开，保存后自动返回
  const fromPrefillRef = React.useRef(false);

  // 连接参数字段（仅 mysql/redis/postgres 使用）
  const [creds, setCreds] = React.useState<Credentials>(emptyCreds());

  async function fetchConnections() {
    setLoading(true);
    setError("");
    try {
      const resp = await fetch("/api/mcp/connections");
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      setConnections(await resp.json());
    } catch (err) {
      setError(err instanceof Error ? err.message : "读取 MCP 连接失败");
    } finally {
      setLoading(false);
    }
  }

  React.useEffect(() => {
    fetchConnections();
  }, []);

  // 从 prefill 打开新增表单
  React.useEffect(() => {
    if (prefill) {
      const form = emptyForm(prefill.type || "mysql");
      form.name = prefill.name;
      // 保留 prefill 带来的 env 模板，但优先用独立字段
      form.env = prefill.env.length > 0 ? prefill.env : form.env;
      form.containerId = prefill.containerId;
      form.nodeletId = prefill.nodeletId;
      setEditing(form);
      setIsNew(true);
      setShowForm(true);
      setTestResult("");
      setSaveError("");
      fromPrefillRef.current = true;

      // 填入连接参数
      const c = emptyCreds();
      if (prefill.host) c.host = prefill.host;
      if (prefill.port) c.port = String(prefill.port);
      if (prefill.user) c.user = prefill.user;
      if (prefill.database) c.database = prefill.database;
      setCreds(c);

      onPrefillConsumed?.();
    }
  }, [prefill, onPrefillConsumed]);

  function openAdd() {
    setIsNew(true);
    setEditing(emptyForm("mysql"));
    setCreds(emptyCreds());
    setShowForm(true);
    setTestResult("");
    setSaveError("");
  }

  function openEdit(item: MCPConnectionStatus) {
    setIsNew(false);
    setEditing({ ...item });
    setCreds(parseCredentials(item.type, item.env));
    setShowForm(true);
    setTestResult("");
    setSaveError("");
  }

  function closeForm() {
    if (saving) return;
    setShowForm(false);
    setEditing(null);
    setTestResult("");
    setSaveError("");
    fromPrefillRef.current = false;
  }

  // 连接参数变化时，自动同步到 env
  function updateCreds(partial: Partial<Credentials>) {
    setCreds((prev) => {
      const next = { ...prev, ...partial };
      setEditing((form) => {
        if (!form) return form;
        const generated = credentialsToEnv(form.type, next);
        if (generated.length === 0) return form;
        // 替换 env 中由凭证生成的条目（保留用户手动添加的额外 env）
        const genKeys = new Set(generated.map((e) => e.split("=")[0]));
        const kept = form.env.filter((e) => !genKeys.has(e.split("=")[0]));
        return { ...form, env: [...generated, ...kept] };
      });
      return next;
    });
  }

  async function handleSave() {
    if (!editing || saving) return;
    setSaving(true);
    setSaveError("");
    const body = formToConfig(editing);

    if (isNew) {
      body.id = editing.name.toLowerCase().replace(/\s+/g, "-").replace(/[^a-z0-9-]/g, "") || `mcp-${Date.now()}`;
    }

    const url = isNew
      ? "/api/mcp/connections"
      : `/api/mcp/connections/${encodeURIComponent(body.id)}`;
    const method = isNew ? "POST" : "PUT";

    try {
      const resp = await fetch(url, {
        method,
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      if (!resp.ok) {
        const data = await resp.json().catch(() => ({ error: `HTTP ${resp.status}` }));
        throw new Error(data.error || `HTTP ${resp.status}`);
      }
      setShowForm(false);
      setEditing(null);
      setTestResult("");
      setSaveError("");
      // 如果是从容器"一键配置 MCP"进来的，保存后自动返回容器详情
      if (fromPrefillRef.current) {
        fromPrefillRef.current = false;
        onGoBack?.();
        return;
      }
      fetchConnections();
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : "保存失败");
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete(id: string) {
    if (!window.confirm(`确定要删除 MCP 连接 "${id}" 吗？`)) return;
    try {
      const resp = await fetch(`/api/mcp/connections/${encodeURIComponent(id)}`, { method: "DELETE" });
      if (!resp.ok) {
        const data = await resp.json().catch(() => ({ error: `HTTP ${resp.status}` }));
        throw new Error(data.error || `HTTP ${resp.status}`);
      }
      fetchConnections();
    } catch (err) {
      setError(err instanceof Error ? err.message : "删除失败");
    }
  }

  async function handleTest() {
    if (!editing) return;
    setTesting(true);
    setTestResult("");
    try {
      const resp = await fetch("/api/mcp/connections/test", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(formToConfig(editing)),
      });
      const data = await resp.json();
      if (data.status === "ok") {
        setTestResult("连接测试成功 ✓");
      } else {
        setTestResult(`连接失败: ${data.error || "未知错误"}`);
      }
    } catch (err) {
      setTestResult(err instanceof Error ? err.message : "测试请求失败");
    } finally {
      setTesting(false);
    }
  }

  async function handleToggleEnabled(item: MCPConnectionStatus) {
    setToggling((prev) => new Set(prev).add(item.id));
    const body: MCPConnectionConfig = {
      id: item.id,
      name: item.name,
      type: item.type,
      command: item.command,
      args: item.args,
      env: item.env,
      enabled: !item.enabled,
    };
    try {
      const resp = await fetch(`/api/mcp/connections/${encodeURIComponent(item.id)}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      fetchConnections();
    } catch (err) {
      setError(err instanceof Error ? err.message : "更新失败");
    } finally {
      setToggling((prev) => {
        const next = new Set(prev);
        next.delete(item.id);
        return next;
      });
    }
  }

  const statusLabel: Record<string, string> = {
    running: "运行中",
    stopped: "已停止",
    error: "异常",
  };

  const showCredentials = typesWithCredentials.has(editing?.type || "");

  return (
    <section className="panel">
      <div className="panel-summary">
        <Wrench size={16} />
        <span>{loading ? "读取中" : `${connections.length} 个 MCP 连接`}</span>
        <button className="primary-button small" onClick={openAdd}>
          <Plus size={15} />
          <span>新增</span>
        </button>
        <button className="ghost-button small" onClick={fetchConnections} disabled={loading}>
          <span>刷新</span>
        </button>
      </div>

      {error && <div className="error-line">{error}</div>}

      <div className="mcp-table-wrap">
        <table>
          <colgroup>
            <col className="mcp-col-name" />
            <col className="mcp-col-type" />
            <col className="mcp-col-status" />
            <col className="mcp-col-count" />
            <col className="mcp-col-command" />
            <col className="mcp-col-toggle" />
            <col className="mcp-col-actions" />
          </colgroup>
          <thead>
            <tr>
              <th>名称</th>
              <th>类型</th>
              <th>状态</th>
              <th>工具数</th>
              <th>命令</th>
              <th>启用</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            {connections.length === 0 && !loading ? (
              <tr>
                <td colSpan={7} className="mcp-table-empty">
                  暂无 MCP 连接，点击"新增"创建
                </td>
              </tr>
            ) : (
              connections.map((item) => (
                <tr key={item.id}>
                  <td className="mcp-name-cell">{item.name}</td>
                  <td>
                    <span className="type-pill">{item.type}</span>
                  </td>
                  <td>
                    <span className={`status-pill ${item.status}`}>
                      {statusLabel[item.status] || item.status}
                    </span>
                    {item.error && <span className="error-hint">{item.error}</span>}
                  </td>
                  <td>{item.toolCount}</td>
                  <td className="mono">{item.command}</td>
                  <td className="mcp-toggle-cell">
                    <label className="tool-toggle">
                      <input
                        type="checkbox"
                        className="toggle-input"
                        checked={item.enabled}
                        disabled={toggling.has(item.id)}
                        onChange={() => handleToggleEnabled(item)}
                      />
                      <span className={`toggle-track ${toggling.has(item.id) ? "toggle-busy" : ""}`}>
                        <span className="toggle-thumb" />
                      </span>
                    </label>
                  </td>
                  <td className="mcp-actions-cell">
                    <div className="mcp-actions">
                      <button className="ghost-button small" aria-label={`编辑 ${item.name}`} onClick={() => openEdit(item)}>
                        <Edit3 size={14} />
                      </button>
                      <button className="ghost-button small danger" aria-label={`删除 ${item.name}`} onClick={() => handleDelete(item.id)}>
                        <Trash2 size={14} />
                      </button>
                    </div>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      {/* 新增 / 编辑模态框 */}
      {showForm && editing && (
        <div className="modal-overlay" onClick={closeForm} onKeyDown={(e) => { if (e.key === "Escape") closeForm(); }}>
          <div className="modal-card" role="dialog" aria-modal="true" aria-labelledby="mcp-form-title" style={{ maxWidth: "560px" }} onClick={(e) => e.stopPropagation()}>
            <div className="modal-head">
              <h2 id="mcp-form-title">{isNew ? "新增 MCP 连接" : "编辑 MCP 连接"}</h2>
              <button className="ghost-button" onClick={closeForm}>
                <X size={18} />
              </button>
            </div>

            <div className="modal-body">
              <label htmlFor="mcp-name">名称</label>
              <input
                id="mcp-name"
                value={editing.name}
                onChange={(e) => setEditing({ ...editing, name: e.target.value })}
                placeholder="例如: 生产环境 MySQL"
              />

              <label htmlFor="mcp-type">类型</label>
              <select
                id="mcp-type"
                value={editing.type}
                onChange={(e) => {
                  const newType = e.target.value;
                  const defaults = typeDefaults[newType] || typeDefaults.other;
                  const newCreds = parseCredentials(newType, defaults.env);
                  setCreds(newCreds);
                  setEditing({
                    ...editing,
                    type: newType,
                    command: defaults.command,
                    args: [...defaults.args],
                    env: [...defaults.env],
                  });
                }}
              >
                <option value="mysql">MySQL</option>
                <option value="redis">Redis</option>
                <option value="postgres">PostgreSQL</option>
                <option value="other">其他</option>
              </select>

              {/* 连接参数 */}
              {showCredentials && (
                <fieldset className="creds-fieldset">
                  <legend>连接参数</legend>
                  <div className="creds-grid">
                    <label htmlFor="mcp-creds-host">主机</label>
                    <input
                      id="mcp-creds-host"
                      value={creds.host}
                      onChange={(e) => updateCreds({ host: e.target.value })}
                      placeholder={editing.type === "redis" ? "127.0.0.1" : "host"}
                    />

                    <label htmlFor="mcp-creds-port">端口</label>
                    <input
                      id="mcp-creds-port"
                      value={creds.port}
                      onChange={(e) => updateCreds({ port: e.target.value })}
                      placeholder={editing.type === "mysql" ? "3306" : editing.type === "postgres" ? "5432" : "6379"}
                    />

                    <label htmlFor="mcp-creds-user">用户</label>
                    <input
                      id="mcp-creds-user"
                      value={creds.user}
                      onChange={(e) => updateCreds({ user: e.target.value })}
                      placeholder={editing.type === "redis" ? "(可选)" : "root"}
                      autoComplete="off"
                    />

                    <label htmlFor="mcp-creds-password">密码</label>
                    <input
                      id="mcp-creds-password"
                      type="password"
                      value={creds.password}
                      onChange={(e) => updateCreds({ password: e.target.value })}
                      placeholder="输入密码"
                      autoComplete="new-password"
                    />

                    <label htmlFor="mcp-creds-database">数据库</label>
                    <input
                      id="mcp-creds-database"
                      value={creds.database}
                      onChange={(e) => updateCreds({ database: e.target.value })}
                      placeholder={editing.type === "redis" ? "0" : editing.type === "mysql" ? "mysql" : "postgres"}
                    />
                  </div>
                </fieldset>
              )}

              <label htmlFor="mcp-command">命令路径</label>
              <input
                id="mcp-command"
                value={editing.command}
                onChange={(e) => setEditing({ ...editing, command: e.target.value })}
                placeholder="mysql-mcp-server"
              />

              <label htmlFor="mcp-args">参数（每行一个）</label>
              <textarea
                id="mcp-args"
                rows={3}
                value={editing.args.join("\n")}
                onChange={(e) => setEditing({ ...editing, args: e.target.value.split("\n").filter(Boolean) })}
                placeholder="--read-only"
              />

              <label htmlFor="mcp-env">环境变量（KEY=VALUE，每行一个）</label>
              <textarea
                id="mcp-env"
                rows={4}
                value={editing.env.join("\n")}
                onChange={(e) => setEditing({ ...editing, env: e.target.value.split("\n").filter(Boolean) })}
                placeholder="MYSQL_DSN=user:pass@tcp(host:3306)/db?charset=utf8mb4"
                spellCheck={false}
              />

              <label className="checkbox-label">
                <input
                  type="checkbox"
                  checked={editing.enabled}
                  onChange={(e) => setEditing({ ...editing, enabled: e.target.checked })}
                />
                <span>启用</span>
              </label>

              {saveError && <div className="error-line">{saveError}</div>}

              {testResult && (
                <div className={`test-result ${testResult.includes("成功") ? "success" : "fail"}`}>
                  {testResult}
                </div>
              )}
            </div>

            <div className="modal-foot">
              <button className="ghost-button" onClick={handleTest} disabled={testing}>
                {testing ? "测试中..." : "测试连接"}
              </button>
              <button className="primary-button" onClick={handleSave} disabled={!editing.name.trim() || saving}>
                {saving ? "保存中..." : "保存"}
              </button>
            </div>
          </div>
        </div>
      )}
    </section>
  );
}
