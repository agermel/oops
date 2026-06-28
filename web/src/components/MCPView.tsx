import React from "react";
import { Plus, Trash2, Wrench, X } from "lucide-react";
import type { MCPConnectionStatus } from "../types";

// typeDefaults 定义了每种连接类型的默认命令、参数和环境变量占位符。
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

// emptyForm 返回一个空白连接配置表单，使用指定类型的默认值。
function emptyForm(type?: string): MCPConnectionStatus {
  const t = type || "mysql";
  const defs = typeDefaults[t] || typeDefaults.other;
  return {
    id: "",
    name: "",
    type: t,
    command: defs.command,
    args: [...defs.args],
    env: [...defs.env],
    enabled: true,
    status: "stopped",
    toolCount: 0,
  };
}

// envPlaceholder 根据类型返回合适的占位提示。
function envPlaceholder(type: string): string {
  switch (type) {
    case "mysql":
      return "MYSQL_DSN=user:pass@tcp(host:3306)/db?charset=utf8mb4";
    case "redis":
      return "REDIS_HOST=127.0.0.1\nREDIS_PORT=6379\nREDIS_DB=0\nREDIS_PWD=";
    case "postgres":
      return "DATABASE_URL=postgres://user:pass@host:5432/db";
    default:
      return "KEY=VALUE（每行一个）";
  }
}

// formToConfig 将表单中的数组字段序列化为后端期望的格式。
function formToConfig(form: MCPConnectionStatus) {
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

export function MCPView({
  connections,
  loading,
  error,
  onRefresh,
}: {
  connections: MCPConnectionStatus[];
  loading: boolean;
  error: string;
  onRefresh: () => void;
}) {
  const [showForm, setShowForm] = React.useState(false);
  const [editing, setEditing] = React.useState<MCPConnectionStatus | null>(null);
  const [testResult, setTestResult] = React.useState("");
  const [testing, setTesting] = React.useState(false);

  function openAdd() {
    setEditing(emptyForm("mysql"));
    setShowForm(true);
    setTestResult("");
  }

  function openEdit(item: MCPConnectionStatus) {
    setEditing({ ...item });
    setShowForm(true);
    setTestResult("");
  }

  function closeForm() {
    setShowForm(false);
    setEditing(null);
    setTestResult("");
  }

  async function handleSave() {
    if (!editing) return;
    const body = formToConfig(editing);

    const isNew = !connections.some((c) => c.id === editing.id);
    if (isNew) {
      body.id = editing.name.toLowerCase().replace(/\s+/g, "-") || `mcp-${Date.now()}`;
    }

    const url = isNew ? "/api/mcp/connections" : `/api/mcp/connections/${encodeURIComponent(body.id)}`;
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
      closeForm();
      onRefresh();
    } catch (err) {
      setTestResult(err instanceof Error ? err.message : "保存失败");
    }
  }

  async function handleDelete(id: string) {
    if (!window.confirm(`确定要删除连接 "${id}" 吗？`)) return;
    try {
      const resp = await fetch(`/api/mcp/connections/${encodeURIComponent(id)}`, { method: "DELETE" });
      if (!resp.ok) {
        const data = await resp.json().catch(() => ({ error: `HTTP ${resp.status}` }));
        throw new Error(data.error || `HTTP ${resp.status}`);
      }
      onRefresh();
    } catch (err) {
      alert(err instanceof Error ? err.message : "删除失败");
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

  return (
    <section className="panel">
      <div className="panel-summary">
        <Wrench size={16} />
        <span>{loading ? "读取中" : `${connections.length} 个 MCP 连接`}</span>
        <button className="primary-button small" onClick={openAdd}>
          <Plus size={15} />
          <span>新增</span>
        </button>
        <button className="ghost-button small" onClick={onRefresh} disabled={loading}>
          <span>刷新</span>
        </button>
      </div>

      {error && <div className="error-line">{error}</div>}

      <div className="table-wrap">
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
            {connections.length === 0 ? (
              <tr>
                <td colSpan={6} className="empty">
                  <span>暂无 MCP 连接，点击 "新增" 添加</span>
                </td>
              </tr>
            ) : (
              connections.map((item) => (
                <tr key={item.id}>
                  <td className="name-cell">{item.name}</td>
                  <td><span className="type-pill">{item.type}</span></td>
                  <td>
                    <span className={`status-pill ${item.status}`}>
                      {item.status === "running" ? "运行中" : item.status === "error" ? "异常" : "已停止"}
                    </span>
                    {item.error && <span className="error-hint">{item.error}</span>}
                  </td>
                  <td>{item.toolCount}</td>
                  <td className="mono">{item.command}</td>
                  <td className="actions">
                    <button className="ghost-button" onClick={() => openEdit(item)}>编辑</button>
                    <button className="ghost-button danger" onClick={() => handleDelete(item.id)}>
                      <Trash2 size={15} />
                    </button>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      {/* 编辑 / 新增 Modal */}
      {showForm && editing && (
        <div className="modal-overlay" onClick={closeForm}>
          <div className="modal-card" onClick={(e) => e.stopPropagation()}>
            <div className="modal-head">
              <h2>{connections.some((c) => c.id === editing.id) ? "编辑 MCP 连接" : "新增 MCP 连接"}</h2>
              <button className="ghost-button" onClick={closeForm}><X size={18} /></button>
            </div>

            <div className="modal-body">
              <label>名称</label>
              <input
                value={editing.name}
                onChange={(e) => setEditing({ ...editing, name: e.target.value })}
                placeholder="例如: 生产 MySQL"
              />

              <label>类型</label>
              <select
                value={editing.type}
                onChange={(e) => {
                  const newType = e.target.value;
                  const defs = typeDefaults[newType] || typeDefaults.other;
                  setEditing({
                    ...editing,
                    type: newType,
                    command: defs.command,
                    args: [...defs.args],
                    env: [...defs.env],
                  });
                }}
              >
                <option value="mysql">MySQL</option>
                <option value="redis">Redis</option>
                <option value="postgres">PostgreSQL</option>
                <option value="other">其他</option>
              </select>

              <label>二进制路径</label>
              <input
                value={editing.command}
                onChange={(e) => setEditing({ ...editing, command: e.target.value })}
                placeholder="mysql-mcp-server"
              />

              <label>启动参数（每行一个）</label>
              <textarea
                rows={3}
                value={editing.args.join("\n")}
                onChange={(e) => setEditing({ ...editing, args: e.target.value.split("\n").filter(Boolean) })}
                placeholder="--read-only"
              />

              <label>环境变量（KEY=VALUE，每行一个）</label>
              <textarea
                rows={4}
                value={editing.env.join("\n")}
                onChange={(e) => setEditing({ ...editing, env: e.target.value.split("\n").filter(Boolean) })}
                placeholder={envPlaceholder(editing.type)}
              />

              <label className="checkbox-label">
                <input
                  type="checkbox"
                  checked={editing.enabled}
                  onChange={(e) => setEditing({ ...editing, enabled: e.target.checked })}
                />
                <span>启用</span>
              </label>

              {testResult && (
                <div className={`test-result ${testResult.includes("成功") ? "success" : "error"}`}>
                  {testResult}
                </div>
              )}
            </div>

            <div className="modal-foot">
              <button className="ghost-button" onClick={handleTest} disabled={testing}>
                {testing ? "测试中..." : "测试连接"}
              </button>
              <button className="primary-button" onClick={handleSave}>保存</button>
            </div>
          </div>
        </div>
      )}
    </section>
  );
}
