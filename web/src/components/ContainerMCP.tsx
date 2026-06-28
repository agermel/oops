import React from "react";
import { Wrench, Plus, Trash2, Edit3 } from "lucide-react";
import type { MCPStatus, MCPConnectionStatus, MCPConnectionConfig, DSNInfo, MCPPrefill } from "../types";
import { apiRequest, getErrorMessage } from "../lib/api";
import { serverPaths } from "../lib/paths";
import { MCPFormModal } from "./MCPFormModal";
import { StatusPill } from "./StatusPill";
import { MCPToolList } from "./MCPToolList";

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

  // 表单模态框控制
  const [showForm, setShowForm] = React.useState(false);
  const [editItem, setEditItem] = React.useState<MCPConnectionStatus | null>(null);
  const [formPrefill, setFormPrefill] = React.useState<MCPPrefill | null>(null);

  const [deleting, setDeleting] = React.useState(false);

  const paths = serverPaths(projectId, nodeletId);
  const mcpUrl = paths.containerMCP(containerId);

  // 从容器-scoped API 获取绑定的 MCP 连接
  async function fetchConnection() {
    setLoading(true);
    setError("");
    try {
      const data = await apiRequest<MCPConnectionStatus | null>(mcpUrl);
      setConnection(data); // null if no connection bound
    } catch (err) {
      setError(getErrorMessage(err, "读取 MCP 连接失败"));
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
  function buildPrefill(): MCPPrefill {
    const env: string[] = [];
    if (dsn?.raw) {
      const type = serviceType;
      if (type === "mysql") env.push(`MYSQL_DSN=${dsn.raw}`);
      else if (type === "redis") {
        if (dsn.host) env.push(`REDIS_HOST=${dsn.host}`);
        if (dsn.port) env.push(`REDIS_PORT=${String(dsn.port)}`);
      } else if (type === "postgres") env.push(`DATABASE_URL=${dsn.raw}`);
      else if (type === "mongo") env.push(`MONGO_URI=${dsn.raw}`);
      else env.push(dsn.raw);
    }
    return {
      name: containerName,
      type: serviceType,
      host: dsn?.host,
      port: dsn?.port,
      user: dsn?.user,
      database: dsn?.database,
      env,
      containerId,
      nodeletId,
    };
  }

  function openAdd() {
    setFormPrefill(buildPrefill());
    setEditItem(null);
    setShowForm(true);
  }

  function openEdit() {
    if (!connection) return;
    setEditItem(connection);
    setFormPrefill(null);
    setShowForm(true);
  }

  // 容器切换时：新建模式下用新容器 DSN 刷新，编辑模式下关闭
  React.useEffect(() => {
    if (showForm) {
      if (!editItem) {
        // 新建模式：用新容器的 DSN 刷新预填
        setFormPrefill(buildPrefill());
      } else {
        // 编辑模式：编辑的是另一个容器的连接，关闭表单
        setShowForm(false);
        setEditItem(null);
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [containerId]);

  async function handleDelete() {
    if (!connection || deleting) return;
    if (!window.confirm(`确定要删除此容器的 MCP 连接 "${connection.name}" 吗？`)) return;
    setDeleting(true);
    try {
      await apiRequest(mcpUrl, { method: "DELETE" });
      setConnection(null);
      onMCPChanged();
    } catch (err) {
      setError(getErrorMessage(err, "删除失败"));
    } finally {
      setDeleting(false);
    }
  }

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
            <StatusPill status={connection.status} labelMap={statusLabel} />
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
                <MCPToolList connectionId={connection.id} tools={connection.tools} />
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

      {/* MCP 表单模态框 */}
      {showForm && (
        <MCPFormModal
          editItem={editItem}
          prefill={formPrefill}
          onClose={() => {
            setShowForm(false);
            setEditItem(null);
            setFormPrefill(null);
          }}
          onSaved={() => {
            fetchConnection();
            onMCPChanged();
          }}
        />
      )}
    </div>
  );
}
