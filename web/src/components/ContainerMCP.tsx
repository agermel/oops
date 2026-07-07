import React from "react";
import { Wrench, Plus, Trash2, Edit3, RotateCw } from "lucide-react";
import type { MCPStatus, MCPConnectionStatus, MCPConnectionConfig, DSNInfo, MCPPrefill, PortMapping } from "../types";
import { mcpStatusLabel } from "../types";
import { useModal } from "../hooks/useModal";
import { apiRequest, getErrorMessage } from "../lib/api";
import { serverPaths } from "../lib/paths";
import { MCPFormModal } from "./MCPFormModal";
import { StatusPill, TypePill } from "./StatusPill";
import { MCPToolList } from "./MCPToolList";
import { Button } from "./ui/Button";

export function ContainerMCP({
  mcp,
  projectId,
  nodeletId,
  containerId,
  containerName,
  serviceType,
  dsn,
  nodeletAddress,
  containerPorts,
  onMCPChanged,
}: {
  mcp?: MCPStatus;
  projectId: string;
  nodeletId: string;
  containerId: string;
  containerName: string;
  serviceType: string;
  dsn?: DSNInfo;
  nodeletAddress?: string;
  containerPorts?: PortMapping[];
  onMCPChanged: () => void;
}) {
  const [connection, setConnection] = React.useState<MCPConnectionStatus | null>(null);
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState("");

  // 表单模态框控制
  const formModal = useModal<MCPConnectionStatus>();
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

  // projectId/nodeletId/containerId/connectionId 变化时重新获取
  React.useEffect(() => {
    fetchConnection();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId, nodeletId, containerId, mcp?.connectionId]);

  function serverIP(): string {
    if (!nodeletAddress) return "";
    try {
      return new URL(nodeletAddress).hostname;
    } catch {
      return nodeletAddress.replace(/^https?:\/\//, "").replace(/:\d+$/, "");
    }
  }

  function publishedPort(): string {
    if (!containerPorts || containerPorts.length === 0) return "";
    for (const p of containerPorts) {
      if (p.hostPort) return p.hostPort;
    }
    return String(containerPorts[0].containerPort || "");
  }

  function buildPrefill(): MCPPrefill {
    const host = dsn?.host || serverIP();
    const port = dsn?.port ? String(dsn.port) : publishedPort();
    const user = dsn?.user || "";
    const database = dsn?.database || "";

    const env: string[] = [];
    const type = serviceType;

    if (type === "mysql") {
      if (dsn?.raw) {
        env.push(`MYSQL_DSN=${dsn.raw}`);
      } else if (host) {
        env.push(`MYSQL_DSN=${user}:@tcp(${host}:${port})/${database}?charset=utf8mb4`);
      }
    } else if (type === "redis") {
      if (host) env.push(`REDIS_HOST=${host}`);
      if (port) env.push(`REDIS_PORT=${port}`);
      if (dsn?.user) env.push(`REDIS_USERNAME=${dsn.user}`);
      if (dsn?.database) env.push(`REDIS_DB=${dsn.database}`);
    } else if (type === "postgres") {
      if (dsn?.raw) {
        env.push(`DATABASE_URL=${dsn.raw}`);
      } else if (host) {
        env.push(`DATABASE_URL=postgres://${user}:@${host}:${port}/${database}`);
      }
    } else if (type === "elasticsearch") {
      if (dsn?.raw) {
        env.push(`ELASTICSEARCH_HOSTS=${dsn.raw}`);
      } else if (host) {
        env.push(`ELASTICSEARCH_HOSTS=http://${host}:${port || "9200"}`);
      }
    } else if (type === "kafka") {
      if (dsn?.raw) {
        env.push(`BOOTSTRAP_SERVERS=${dsn.raw}`);
      } else if (host) {
        env.push(`BOOTSTRAP_SERVERS=${host}:${port || "9092"}`);
      }
    } else if (type === "mongo" && dsn?.raw) {
      env.push(`MONGO_URI=${dsn.raw}`);
    }

    return {
      name: containerName,
      type: serviceType,
      host: host || undefined,
      port: port ? Number(port) : undefined,
      user: dsn?.user,
      database: dsn?.database,
      env,
      containerId,
      nodeletId,
    };
  }

  function openAdd() {
    setFormPrefill(buildPrefill());
    formModal.onOpen();
  }

  function openEdit() {
    if (!connection) return;
    setFormPrefill(null);
    formModal.onOpen(connection);
  }

  // 容器切换时：新建模式下用新容器 DSN 刷新，编辑模式下关闭
  React.useEffect(() => {
    if (formModal.open) {
      if (!formModal.data) {
        setFormPrefill(buildPrefill());
      } else {
        formModal.onClose();
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId, nodeletId, containerId]);

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
      <div className="section-header">
        <Wrench size={18} />
        <h2>MCP 连接</h2>
        <Button variant="ghost" size="sm" onClick={fetchConnection} disabled={loading} title="刷新 MCP 连接状态">
          <RotateCw size={14} className={loading ? "spin" : ""} />
          <span>刷新</span>
        </Button>
      </div>

      {error && <div className="error-banner">{error}</div>}

      {/* 加载中 */}
      {loading && <div className="empty-state">正在加载 MCP 配置</div>}

      {/* 无连接 - 显示空状态 */}
      {!loading && !connection && !formModal.open && (
        <div className="mcp-status-card">
          <div className="mcp-config-empty">
            <p>MCP 连接未配置</p>
            <Button size="sm" onClick={openAdd}>
              <Plus size={14} />
              <span>一键配置 MCP</span>
            </Button>
          </div>
        </div>
      )}

      {/* 有连接 - 显示完整配置 */}
      {!loading && connection && !formModal.open && (
        <div className="mcp-status-card">
          <div className="mcp-status-row">
            <span>状态</span>
            <StatusPill status={connection.status} labelMap={mcpStatusLabel} />
          </div>
          <div className="mcp-status-row">
            <span>名称</span>
            <strong>{connection.name}</strong>
          </div>
          <div className="mcp-status-row">
            <span>类型</span>
            <TypePill label={connection.type} />
          </div>
          <div className="mcp-status-row">
            <span>传输</span>
            <code className="mono">{connection.transport || "stdio"}</code>
          </div>
          {connection.transport === "sse" ? (
            <div className="mcp-status-row">
              <span>SSE 地址</span>
              <code className="mono">{connection.url}</code>
            </div>
          ) : (
            <>
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
            </>
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
                <MCPToolList connectionId={connection.id} tools={connection.tools} onRefreshTools={fetchConnection} />
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
            <Button variant="ghost" size="sm" onClick={openEdit}>
              <Edit3 size={14} />
              <span>编辑</span>
            </Button>
            <Button variant="ghost" size="sm" danger onClick={handleDelete} disabled={deleting}>
              <Trash2 size={14} />
              <span>{deleting ? "删除中..." : "删除"}</span>
            </Button>
          </div>
        </div>
      )}

      {/* MCP 表单模态框 */}
      {formModal.open && (
        <MCPFormModal
          editItem={formModal.data}
          prefill={formPrefill}
          projectId={projectId}
          containerOptions={[{
            kind: "container",
            nodeletId,
            nodeletName: nodeletId,
            containerId,
            containerName,
            serviceType,
          }]}
          onClose={() => {
            formModal.onClose();
            setFormPrefill(null);
          }}
          onSaved={() => {
            formModal.onClose();
            setFormPrefill(null);
            fetchConnection();
            onMCPChanged();
          }}
          onTested={() => {
            fetchConnection();
            onMCPChanged();
          }}
        />
      )}
    </div>
  );
}
