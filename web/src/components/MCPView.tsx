import React from "react";
import { Plus, Trash2, Edit3, Wrench, X } from "lucide-react";
import type { MCPConnectionConfig, MCPConnectionStatus, MCPPrefill } from "../types";

// ---- 类型默认值 ----
const typeDefaults: Record<string, { command: string; args: string[]; env: string[] }> = {
  mysql: {
    command: "./bin/mysql-mcp-server",
    args: ["--silent"],
    env: ["MYSQL_DSN=user:pass@tcp(host:3306)/db?charset=utf8mb4"],
  },
  redis: {
    command: "./bin/redis-mcp-server",
    args: [],
    env: ["REDIS_HOST=127.0.0.1", "REDIS_PORT=6379", "REDIS_DB=0", "REDIS_PWD="],
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
  };
}

// ---- MCPView ----
export function MCPView({ prefill, onPrefillConsumed }: { prefill?: MCPPrefill | null; onPrefillConsumed?: () => void }) {
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

  React.useEffect(() => {
    if (prefill) {
      const form = emptyForm(prefill.type || "mysql");
      form.name = prefill.name;
      form.env = prefill.env;
      setEditing(form);
      setIsNew(true);
      setShowForm(true);
      setTestResult("");
      setSaveError("");
      onPrefillConsumed?.();
    }
  }, [prefill, onPrefillConsumed]);

  function openAdd() {
    setIsNew(true);
    setEditing(emptyForm("mysql"));
    setShowForm(true);
    setTestResult("");
    setSaveError("");
  }

  function openEdit(item: MCPConnectionStatus) {
    setIsNew(false);
    setEditing({ ...item });
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

  const statusLabel: Record<string, string> = {
    running: "运行中",
    stopped: "已停止",
    error: "异常",
  };

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
          <thead>
            <tr>
              <th>名称</th>
              <th>类型</th>
              <th>状态</th>
              <th>工具数</th>
              <th>命令</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            {connections.length === 0 && !loading ? (
              <tr>
                <td colSpan={6} className="mcp-table-empty">
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
                  <td className="mcp-actions">
                    <button className="ghost-button small" aria-label={`编辑 ${item.name}`} onClick={() => openEdit(item)}>
                      <Edit3 size={14} />
                    </button>
                    <button className="ghost-button small danger" aria-label={`删除 ${item.name}`} onClick={() => handleDelete(item.id)}>
                      <Trash2 size={14} />
                    </button>
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
          <div className="modal-card" role="dialog" aria-modal="true" aria-labelledby="mcp-form-title" style={{ maxWidth: "520px" }} onClick={(e) => e.stopPropagation()}>
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
