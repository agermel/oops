import React from "react";
import { Wrench, Plus, Trash2, Edit3, X, Play } from "lucide-react";
import type { MCPStatus, MCPConnectionStatus, MCPConnectionConfig, DSNInfo, ToolTestResult } from "../types";

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

const typesWithCredentials = new Set(["mysql", "redis", "postgres", "etcd"]);

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

function credentialsToEnv(type: string, creds: Credentials): string[] {
  if (type === "mysql") {
    const pass = creds.password ? `:${creds.password}` : "";
    const dsn = `${creds.user}${pass}@tcp(${creds.host}:${creds.port})/${creds.database}?charset=utf8mb4`;
    if (!creds.user && !creds.host) return [];
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

const statusLabel: Record<string, string> = {
  running: "运行中",
  stopped: "已停止",
  error: "异常",
};

export function ContainerMCP({
  mcp,
  projectId,
  nodeletId,
  containerId,
  containerName,
  serviceType,
  dsn,
  onMCPChanged,
}: {
  mcp?: MCPStatus;
  projectId: string;
  nodeletId: string;
  containerId: string;
  containerName: string;
  serviceType: string;
  dsn?: DSNInfo;
  onMCPChanged: () => void;
}) {
  const [connection, setConnection] = React.useState<MCPConnectionStatus | null>(null);
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
  const [deleting, setDeleting] = React.useState(false);
  const [toolTests, setToolTests] = React.useState<Record<string, ToolTestResult>>({});
  const [testingTools, setTestingTools] = React.useState<Set<string>>(new Set());
  const [creds, setCreds] = React.useState<Credentials>(emptyCreds());

  // 从容器-scoped API 获取绑定的 MCP 连接
  async function fetchConnection() {
    setLoading(true);
    setError("");
    try {
      const pid = encodeURIComponent(projectId);
      const nid = encodeURIComponent(nodeletId);
      const cid = encodeURIComponent(containerId);
      const resp = await fetch(`/api/projects/${pid}/servers/${nid}/containers/${cid}/mcp`);
      if (!resp.ok) {
        const data = await resp.json().catch(() => ({ error: `HTTP ${resp.status}` }));
        throw new Error(data.error || `HTTP ${resp.status}`);
      }
      const data = await resp.json();
      setConnection(data); // null if no connection bound
    } catch (err) {
      setError(err instanceof Error ? err.message : "读取 MCP 连接失败");
    } finally {
      setLoading(false);
    }
  }

  // 首次加载 / connectionId 变化时获取
  React.useEffect(() => {
    fetchConnection();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mcp?.connectionId]);

  // 构建一键配置预填
  function buildPrefillEnv(): string[] {
    const env: string[] = [];
    if (dsn?.raw) {
      const type = serviceType;
      if (type === "mysql") env.push(`MYSQL_DSN=${dsn.raw}`);
      else if (type === "redis") {
        env.push(`REDIS_URL=${dsn.raw}`);
        if (dsn.host) env.push(`REDIS_HOST=${dsn.host}`);
        if (dsn.port) env.push(`REDIS_PORT=${String(dsn.port)}`);
      } else if (type === "postgres") env.push(`DATABASE_URL=${dsn.raw}`);
      else if (type === "mongo") env.push(`MONGO_URI=${dsn.raw}`);
      else env.push(dsn.raw);
    }
    return env;
  }

  function openAdd() {
    const prefillEnv = buildPrefillEnv();
    const form = emptyForm(serviceType || "mysql");
    form.name = containerName;
    form.env = prefillEnv.length > 0 ? prefillEnv : form.env;
    form.containerId = containerId;
    form.nodeletId = nodeletId;
    setEditing(form);
    setIsNew(true);
    setShowForm(true);
    setTestResult("");
    setSaveError("");

    const c = emptyCreds();
    if (dsn?.host) c.host = dsn.host;
    if (dsn?.port) c.port = String(dsn.port);
    if (dsn?.user) c.user = dsn.user;
    if (dsn?.database) c.database = dsn.database;
    setCreds(c);
  }

  function openEdit() {
    if (!connection) return;
    setIsNew(false);
    setEditing({ ...connection });
    setCreds(parseCredentials(connection.type, connection.env));
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
  }

  function updateCreds(partial: Partial<Credentials>) {
    setCreds((prev) => {
      const next = { ...prev, ...partial };
      setEditing((form) => {
        if (!form) return form;
        const generated = credentialsToEnv(form.type, next);
        if (generated.length === 0) return form;
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
      body.containerId = containerId;
      body.nodeletId = nodeletId;
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
      fetchConnection();
      onMCPChanged();
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : "保存失败");
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete() {
    if (!connection || deleting) return;
    if (!window.confirm(`确定要删除此容器的 MCP 连接 "${connection.name}" 吗？`)) return;
    setDeleting(true);
    try {
      const pid = encodeURIComponent(projectId);
      const nid = encodeURIComponent(nodeletId);
      const cid = encodeURIComponent(containerId);
      const resp = await fetch(`/api/projects/${pid}/servers/${nid}/containers/${cid}/mcp`, { method: "DELETE" });
      if (!resp.ok) {
        const data = await resp.json().catch(() => ({ error: `HTTP ${resp.status}` }));
        throw new Error(data.error || `HTTP ${resp.status}`);
      }
      setConnection(null);
      onMCPChanged();
    } catch (err) {
      setError(err instanceof Error ? err.message : "删除失败");
    } finally {
      setDeleting(false);
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

  async function testTool(toolName: string) {
    if (!connection) return;
    const key = `${connection.id}:${toolName}`;
    setTestingTools((prev) => new Set(prev).add(key));
    setToolTests((prev) => ({ ...prev, [key]: { status: "testing" } }));
    try {
      const resp = await fetch(`/api/mcp/connections/${encodeURIComponent(connection.id)}/tools/${encodeURIComponent(toolName)}/test`, { method: "POST" });
      const data = await resp.json();
      if (data.status === "ok") {
        setToolTests((prev) => ({ ...prev, [key]: { status: "ok", output: data.output } }));
      } else {
        setToolTests((prev) => ({ ...prev, [key]: { status: "error", error: data.error } }));
      }
    } catch (err) {
      setToolTests((prev) => ({ ...prev, [key]: { status: "error", error: err instanceof Error ? err.message : "测试失败" } }));
    } finally {
      setTestingTools((prev) => {
        const next = new Set(prev);
        next.delete(key);
        return next;
      });
    }
  }

  const showCredentials = typesWithCredentials.has(editing?.type || "");

  // ---- 渲染 ----

  return (
    <div className="container-mcp">
      <div className="section-title">
        <Wrench size={18} />
        <h2>MCP 连接</h2>
      </div>

      {error && <div className="error-line">{error}</div>}

      {/* 加载中 */}
      {loading && <div className="empty-card">正在加载 MCP 配置</div>}

      {/* 无连接 - 显示空状态 */}
      {!loading && !connection && !showForm && (
        <div className="mcp-status-card">
          <div className="mcp-config-empty">
            <p>MCP 连接未配置</p>
            <button className="primary-button small" onClick={openAdd}>
              <Plus size={14} />
              <span>一键配置 MCP</span>
            </button>
          </div>
        </div>
      )}

      {/* 有连接 - 显示完整配置 */}
      {!loading && connection && !showForm && (
        <div className="mcp-status-card">
          <div className="mcp-status-row">
            <span>状态</span>
            <span className={`status-pill ${connection.status}`}>
              {statusLabel[connection.status] || connection.status}
            </span>
          </div>
          <div className="mcp-status-row">
            <span>名称</span>
            <strong>{connection.name}</strong>
          </div>
          <div className="mcp-status-row">
            <span>类型</span>
            <span className="type-pill">{connection.type}</span>
          </div>
          <div className="mcp-status-row">
            <span>命令</span>
            <code className="mono">{connection.command}</code>
          </div>
          {connection.args.length > 0 && (
            <div className="mcp-status-row">
              <span>参数</span>
              <code className="mono">{connection.args.join(" ")}</code>
            </div>
          )}
          <div className="mcp-status-row">
            <span>环境变量</span>
            <code className="mono mcp-env-preview">
              {connection.env.length > 0
                ? connection.env.map((e) => {
                    // 隐藏密码
                    const idx = e.indexOf("=");
                    if (idx < 0) return e;
                    const key = e.slice(0, idx);
                    const val = e.slice(idx + 1);
                    if (key.endsWith("PWD") || key.endsWith("PASSWORD") || key === "REDIS_PASSWORD") {
                      return `${key}=****`;
                    }
                    return e;
                  }).join(", ")
                : "(无)"}
            </code>
          </div>
          {connection.status === "running" && (
            <div className="mcp-status-row">
              <span>工具数</span>
              <strong>{connection.toolCount}</strong>
            </div>
          )}
          {connection.status === "running" && connection.tools && connection.tools.length > 0 && (
            <div className="mcp-tools-section">
              <div className="mcp-tools-section-title">工具列表</div>
              <div className="mcp-tools-list">
                <table className="mcp-tools-subtable">
                  <thead>
                    <tr>
                      <th className="mcp-tool-col-status">状态</th>
                      <th className="mcp-tool-col-name">名称</th>
                      <th className="mcp-tool-col-desc">描述</th>
                      <th className="mcp-tool-col-test">测试</th>
                    </tr>
                  </thead>
                  <tbody>
                    {connection.tools.map((t) => {
                      const tkey = `${connection.id}:${t.name}`;
                      const tr = toolTests[tkey];
                      const testing = testingTools.has(tkey);
                      const showDetail = tr && (tr.status === "ok" || tr.status === "error");
                      return (
                        <React.Fragment key={t.name}>
                          <tr className="mcp-tool-row">
                            <td>
                              {testing ? (
                                <span className="tool-status tool-testing" title="测试中…">⟳</span>
                              ) : tr?.status === "ok" ? (
                                <span className="tool-status tool-ok" title="测试通过">✓</span>
                              ) : tr?.status === "error" ? (
                                <span className="tool-status tool-err" title="测试失败">✗</span>
                              ) : (
                                <span className="tool-status tool-untested" title="未测试">○</span>
                              )}
                            </td>
                            <td><code>{t.name}</code></td>
                            <td className="mcp-tool-desc">{t.description}</td>
                            <td>
                              <button
                                className="ghost-button small"
                                disabled={testing}
                                onClick={() => testTool(t.name)}
                              >
                                <Play size={12} />
                                <span>{testing ? "测试中" : "测试"}</span>
                              </button>
                            </td>
                          </tr>
                          {showDetail && (
                            <tr className="tool-test-detail-row">
                              <td colSpan={4}>
                                <div className={`tool-test-detail-body ${tr!.status}`}>
                                  {tr!.status === "error" ? tr!.error : tr!.output}
                                </div>
                              </td>
                            </tr>
                          )}
                        </React.Fragment>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            </div>
          )}
          {connection.error && (
            <div className="mcp-status-row">
              <span>错误</span>
              <span className="error-hint">{connection.error}</span>
            </div>
          )}
          <div className="mcp-status-actions">
            <button className="ghost-button small" onClick={openEdit}>
              <Edit3 size={14} />
              <span>编辑</span>
            </button>
            <button className="ghost-button small danger" onClick={handleDelete} disabled={deleting}>
              <Trash2 size={14} />
              <span>{deleting ? "删除中..." : "删除"}</span>
            </button>
          </div>
        </div>
      )}

      {/* 新增 / 编辑表单 (内联) */}
      {showForm && editing && (
        <div className="mcp-status-card mcp-form-card">
          <div className="mcp-form-head">
            <h3>{isNew ? "新增 MCP 连接" : "编辑 MCP 连接"}</h3>
            <button className="ghost-button" onClick={closeForm}>
              <X size={16} />
            </button>
          </div>

          <div className="mcp-form-body">
            <label htmlFor="cmcp-name">名称</label>
            <input
              id="cmcp-name"
              value={editing.name}
              onChange={(e) => setEditing({ ...editing, name: e.target.value })}
              placeholder="例如: 生产环境 MySQL"
            />

            <label htmlFor="cmcp-type">类型</label>
            <select
              id="cmcp-type"
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

            {showCredentials && (
              <fieldset className="creds-fieldset">
                <legend>连接参数</legend>
                <div className="creds-grid">
                  <label htmlFor="cmcp-creds-host">主机</label>
                  <input
                    id="cmcp-creds-host"
                    value={creds.host}
                    onChange={(e) => updateCreds({ host: e.target.value })}
                    placeholder={editing.type === "redis" ? "127.0.0.1" : "host"}
                  />

                  <label htmlFor="cmcp-creds-port">端口</label>
                  <input
                    id="cmcp-creds-port"
                    value={creds.port}
                    onChange={(e) => updateCreds({ port: e.target.value })}
                    placeholder={editing.type === "mysql" ? "3306" : editing.type === "postgres" ? "5432" : "6379"}
                  />

                  <label htmlFor="cmcp-creds-user">用户</label>
                  <input
                    id="cmcp-creds-user"
                    value={creds.user}
                    onChange={(e) => updateCreds({ user: e.target.value })}
                    placeholder={editing.type === "redis" ? "(可选)" : "root"}
                    autoComplete="off"
                  />

                  <label htmlFor="cmcp-creds-password">密码</label>
                  <input
                    id="cmcp-creds-password"
                    type="password"
                    value={creds.password}
                    onChange={(e) => updateCreds({ password: e.target.value })}
                    placeholder="输入密码"
                    autoComplete="new-password"
                  />

                  <label htmlFor="cmcp-creds-database">数据库</label>
                  <input
                    id="cmcp-creds-database"
                    value={creds.database}
                    onChange={(e) => updateCreds({ database: e.target.value })}
                    placeholder={editing.type === "redis" ? "0" : editing.type === "mysql" ? "mysql" : "postgres"}
                  />
                </div>
              </fieldset>
            )}

            <label htmlFor="cmcp-command">命令路径</label>
            <input
              id="cmcp-command"
              value={editing.command}
              onChange={(e) => setEditing({ ...editing, command: e.target.value })}
              placeholder="mysql-mcp-server"
            />

            <label htmlFor="cmcp-args">参数（每行一个）</label>
            <textarea
              id="cmcp-args"
              rows={3}
              value={editing.args.join("\n")}
              onChange={(e) => setEditing({ ...editing, args: e.target.value.split("\n").filter(Boolean) })}
              placeholder="--read-only"
            />

            <label htmlFor="cmcp-env">环境变量（KEY=VALUE，每行一个）</label>
            <textarea
              id="cmcp-env"
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

          <div className="mcp-form-foot">
            <button className="ghost-button" onClick={handleTest} disabled={testing}>
              {testing ? "测试中..." : "测试连接"}
            </button>
            <button className="primary-button" onClick={handleSave} disabled={!editing.name.trim() || saving}>
              {saving ? "保存中..." : "保存"}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
