import React from "react";
import { Info, Wrench, FileText, Settings, Edit3, Trash2, ArrowRight } from "lucide-react";
import type { ContainerDetail as ContainerDetailType, LogEntry, ProjectMCPConnection, MCPConnectionConfig } from "../types";
import { serviceTypeIcons, mcpStatusLabel } from "../types";
import { useContainerDetail } from "../hooks/useContainerDetail";
import { getErrorMessage, apiRequest } from "../lib/api";
import { mcpConnectionPaths } from "../lib/paths";
import { queryKeys } from "../hooks/queries";
import { useSet } from "../hooks/useSet";
import { useQueryClient } from "@tanstack/react-query";
import { ContainerOverview } from "./ContainerOverview";
import { ContainerMCP } from "./ContainerMCP";
import { ContainerLogs } from "./ContainerLogs";
import { ContainerDSN } from "./ContainerDSN";
import { StatusDot } from "./StatusPill";
import { ToggleSwitch } from "./ToggleSwitch";
import { MCPToolList } from "./MCPToolList";
import { Button } from "./ui/Button";

const CONTAINER_TABS = [
  { id: "overview", label: "概览", icon: Info },
  { id: "dsn", label: "DSN 配置", icon: Settings },
  { id: "mcp", label: "MCP", icon: Wrench },
  { id: "logs", label: "日志", icon: FileText },
] as const;

const MCP_TABS = [
  { id: "overview", label: "概览", icon: Info },
  { id: "tools", label: "工具列表", icon: Wrench },
] as const;

type ContainerTabID = (typeof CONTAINER_TABS)[number]["id"];
type MCPTabID = (typeof MCP_TABS)[number]["id"];

export function ContainerDetailView({
  logs,
  logsLoading,
  logsError,
  autoScroll,
  onAutoScrollChange,
  onClearLogs,
  logsPanelRef,
  nodeletId,
  containerId,
  projectId,
  nodeletAddress,
  mcpConnection,
  onEditMCP,
  onMCPDeleted,
  onNavigateToContainer,
  onMCPChanged,
}: {
  logs: LogEntry[];
  logsLoading: boolean;
  logsError: string;
  autoScroll: boolean;
  onAutoScrollChange: (v: boolean) => void;
  onClearLogs: () => void;
  logsPanelRef: React.RefObject<HTMLDivElement | null>;
  nodeletId: string;
  containerId: string;
  projectId: string;
  nodeletAddress?: string;
  mcpConnection?: ProjectMCPConnection;
  onEditMCP?: (conn: ProjectMCPConnection) => void;
  onMCPDeleted?: () => void;
  onNavigateToContainer?: (nodeletId: string, containerId: string) => void;
  onMCPChanged: () => void;
}) {
  const [activeTab, setActiveTab] = React.useState<ContainerTabID | MCPTabID>("overview");

  const { data: detail, isLoading: loading, error: queryError } = useContainerDetail(
    projectId, nodeletId, containerId
  );
  const error = queryError ? getErrorMessage(queryError, "读取容器详情失败") : "";

  // Reset tab when switching between container and MCP
  React.useEffect(() => { setActiveTab("overview"); }, [containerId, mcpConnection?.id]);

  function handleEditDSN() {
    setActiveTab("dsn");
  }

  // Render MCP connection detail
  if (mcpConnection) {
    return <MCPDetailView projectId={projectId} conn={mcpConnection} activeTab={activeTab as MCPTabID} onTabChange={setActiveTab} onEdit={onEditMCP} onDeleted={onMCPDeleted} onNavigateToContainer={onNavigateToContainer} />;
  }

  // Render container detail
  return (
    <div className="container-detail">
      <div className="detail-tabs" role="tablist" aria-label="容器详情标签页">
        {CONTAINER_TABS.map((tab) => (
          <button
            key={tab.id}
            role="tab"
            aria-selected={activeTab === tab.id}
            className={activeTab === tab.id ? "active" : ""}
            onClick={() => setActiveTab(tab.id)}
          >
            <tab.icon size={15} />
            <span>{tab.label}</span>
          </button>
        ))}
      </div>

      {error && <div className="error-banner">{error}</div>}

      <div className="detail-body">
        {loading && !detail ? (
          <div className="loading-overlay"><span className="spinner" /> 加载容器详情...</div>
        ) : !detail ? (
          <div className="empty-state">从左侧选择一个容器查看详情</div>
        ) : (
          <>
            {activeTab === "overview" && (
              <div role="tabpanel"><ContainerOverview detail={detail} onEditDSN={handleEditDSN} /></div>
            )}
            {activeTab === "dsn" && (
              <div role="tabpanel">
                <ContainerDSN
                  projectId={projectId}
                  nodeletId={nodeletId}
                  containerId={containerId}
                  serviceType={detail.serviceType}
                  onChanged={onMCPChanged}
                />
              </div>
            )}
            {activeTab === "mcp" && (
              <div role="tabpanel">
                <ContainerMCP
                  mcp={detail.mcp}
                  projectId={projectId}
                  nodeletId={nodeletId}
                  containerId={containerId}
                  containerName={detail.container.name}
                  serviceType={detail.serviceType}
                  dsn={detail.dsn}
                  nodeletAddress={nodeletAddress}
                  containerPorts={detail.container.ports}
                  onMCPChanged={onMCPChanged}
                />
              </div>
            )}
            {activeTab === "logs" && (
              <div role="tabpanel">
                <ContainerLogs
                  key={containerId}
                  logs={logs}
                  loading={logsLoading}
                  error={logsError}
                  autoScroll={autoScroll}
                  onAutoScrollChange={onAutoScrollChange}
                  onClear={onClearLogs}
                  panelRef={logsPanelRef}
                />
              </div>
            )}
          </>
        )}
      </div>
    </div>
  );
}

// ---- MCP 详情视图（内嵌在 ContainerDetailView 中）----

function MCPDetailView({
  projectId,
  conn,
  activeTab,
  onTabChange,
  onEdit,
  onDeleted,
  onNavigateToContainer,
}: {
  projectId: string;
  conn: ProjectMCPConnection;
  activeTab: MCPTabID;
  onTabChange: (id: MCPTabID) => void;
  onEdit?: (conn: ProjectMCPConnection) => void;
  onDeleted?: () => void;
  onNavigateToContainer?: (nodeletId: string, containerId: string) => void;
}) {
  const Icon = serviceTypeIcons[conn.type] || serviceTypeIcons.unknown;
  const queryClient = useQueryClient();
  const toggling = useSet();

  async function handleDelete() {
    if (!window.confirm(`确定要删除 MCP 连接 "${conn.name}" 吗？`)) return;
    try {
      await apiRequest(mcpConnectionPaths.detail(conn.id), { method: "DELETE" });
      onDeleted?.();
    } catch (err) {
      alert(getErrorMessage(err, "删除失败"));
    }
  }

  async function handleToggle(enabled: boolean) {
    toggling.add(conn.id);
    const body: MCPConnectionConfig = {
      id: conn.id,
      name: conn.name,
      type: conn.type,
      transport: conn.transport,
      command: conn.command,
      args: conn.args,
      env: conn.env,
      url: conn.url,
      enabled,
      containerId: conn.containerId,
      nodeletId: conn.nodeletId,
    };
    try {
      await apiRequest(mcpConnectionPaths.detail(conn.id), {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      queryClient.invalidateQueries({ queryKey: queryKeys.mcp.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.mcp.byProject(projectId) });
    } catch (err) {
      alert(getErrorMessage(err, "更新失败"));
    } finally {
      toggling.remove(conn.id);
    }
  }

  return (
    <div className="container-detail">
      {/* Tab bar */}
      <div className="detail-tabs" role="tablist" aria-label="MCP 连接详情标签页">
        {MCP_TABS.map((tab) => (
          <button
            key={tab.id}
            role="tab"
            aria-selected={activeTab === tab.id}
            className={activeTab === tab.id ? "active" : ""}
            onClick={() => onTabChange(tab.id)}
          >
            <tab.icon size={15} />
            <span>{tab.label}</span>
          </button>
        ))}
        <div className="mcp-detail-header-actions">
          <Button variant="ghost" size="sm" aria-label={`编辑 ${conn.name}`} onClick={() => onEdit?.(conn)}>
            <Edit3 size={14} />
          </Button>
          <Button variant="ghost" size="sm" danger aria-label={`删除 ${conn.name}`} onClick={handleDelete}>
            <Trash2 size={14} />
          </Button>
        </div>
      </div>

      <div className="detail-body" style={{ overflowY: "auto" }}>
        {activeTab === "overview" && (
          <div role="tabpanel">
            <div className="container-overview">
              <div className="overview-header">
                <Icon size={28} />
                <div className="overview-header-content">
                  <h2>{conn.name}</h2>
                  <div className="overview-header-badges">
                    <span className={`status-pill ${conn.status || "stopped"}`}>
                      {mcpStatusLabel[conn.status || "stopped"] || "未知"}
                    </span>
                  </div>
                </div>
              </div>

              <div className="overview-grid">
                {/* 连接信息 */}
                <div className="overview-card">
                  <h3>连接信息</h3>
                  <dl>
                    <dt>名称</dt>
                    <dd>{conn.name}</dd>
                    <dt>类型</dt>
                    <dd>{conn.type}</dd>
                    <dt>传输</dt>
                    <dd className="mono">{conn.transport || "stdio"}</dd>
                    {conn.transport === "sse" ? (
                      <><dt>URL</dt><dd className="mono">{conn.url || "-"}</dd></>
                    ) : (
                      <><dt>命令</dt><dd className="mono">{conn.command || "-"}</dd></>
                    )}
                    {conn.args && conn.args.length > 0 && (
                      <><dt>参数</dt><dd className="mono">{conn.args.join(" ")}</dd></>
                    )}
                    {conn.env && conn.env.length > 0 && (
                      <><dt>环境变量</dt><dd>
                        <pre className="mcp-env-raw">{conn.env.join("\n")}</pre>
                      </dd></>
                    )}
                    {(conn.scope === "container" || conn.containerId) && (
                      <><dt>容器绑定</dt>
                      <dd>
                        {conn.containerId && conn.nodeletId ? (
                          <button
                            className="mcp-bound-link"
                            onClick={() => onNavigateToContainer?.(conn.nodeletId!, conn.containerId!)}
                          >
                            {conn.containerId.slice(0, 12)}
                            <ArrowRight size={11} />
                          </button>
                        ) : (
                          <span className="mono">{conn.containerId?.slice(0, 12) || "-"}</span>
                        )}
                      </dd></>
                    )}
                  </dl>
                </div>

                {/* 运行状态 */}
                <div className="overview-card">
                  <h3>运行状态</h3>
                  <dl>
                    <dt>状态</dt>
                    <dd className="mcp-status-row">
                      <StatusDot alive={conn.status === "running"} unknown={conn.status !== "running" && conn.status !== "stopped"} />
                      <span style={{ marginLeft: 6 }}>{mcpStatusLabel[conn.status || "stopped"]}</span>
                    </dd>
                    <dt>工具数</dt>
                    <dd>{conn.toolCount}</dd>
                    {conn.error && (
                      <><dt>错误</dt><dd className="error-text">{conn.error}</dd></>
                    )}
                    <dt>启用</dt>
                    <dd>
                      <ToggleSwitch
                        checked={conn.enabled}
                        disabled={toggling.set.has(conn.id)}
                        onChange={handleToggle}
                      />
                    </dd>
                  </dl>
                </div>
              </div>
            </div>
          </div>
        )}

        {activeTab === "tools" && (
          <div role="tabpanel">
            {conn.status === "running" && conn.tools && conn.tools.length > 0 ? (
              <div className="overview-card" style={{ marginTop: 0 }}>
                <div className="overview-card-head">
                  <h3>工具列表</h3>
                </div>
                <MCPToolList
                  connectionId={conn.id}
                  tools={conn.tools!}
                  onRefreshTools={() => queryClient.invalidateQueries({ queryKey: queryKeys.mcp.byProject(projectId) })}
                />
              </div>
            ) : (
              <div className="empty-state">无运行中的工具</div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
