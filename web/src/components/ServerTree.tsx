import { Server, ChevronDown, ChevronRight, Plus, Trash2, EyeOff, Eye, Wrench } from "lucide-react";
import React from "react";
import type { ServerWithNodelet, ProjectMCPConnection } from "../types";
import { serviceTypeIcons, serviceLabel, mcpStatusLabel } from "../types";
import { getErrorMessage } from "../lib/api";
import { useDeleteServer, useAddServer, useExcludeContainer, useIncludeContainer, useContainers, useProjectMCPConnections } from "../hooks/useServers";
import { useNodelets } from "../hooks/useNodelets";
import { Modal } from "./Modal";
import { StatusDot } from "./StatusPill";
import { Button } from "./ui/Button";

export function ServerTree({
  projectId,
  servers,
  serversLoading,
  serverError,
  selectedContainerID,
  expandedServers,
  excludedContainerRefs,
  onToggleServer,
  onSelectContainer,
}: {
  projectId: string;
  servers: ServerWithNodelet[];
  serversLoading: boolean;
  serverError: string;
  selectedContainerID: string;
  expandedServers: Set<string>;
  excludedContainerRefs?: string[];
  onToggleServer: (nodeletID: string) => void;
  onSelectContainer: (nodeletID: string, containerID: string) => void;
}) {
  const [showAddModal, setShowAddModal] = React.useState(false);

  const { data: nodelets = [], isLoading: nodeletsLoading } = useNodelets();
  const deleteServer = useDeleteServer(projectId);
  const addServer = useAddServer(projectId);
  const excludeContainer = useExcludeContainer(projectId);
  const includeContainer = useIncludeContainer(projectId);

  const {
    data: mcpConns = [],
    isLoading: mcpLoading,
    error: mcpQueryError,
  } = useProjectMCPConnections(projectId);
  const mcpError = mcpQueryError ? getErrorMessage(mcpQueryError, "读取 MCP 连接失败") : "";

  // Build lookup maps from MCP connections keyed by nodeletId and nodeletId:containerId.
  const mcpByNodelet = React.useMemo(() => {
    const byNodelet: Record<string, ProjectMCPConnection[]> = {};
    for (const c of mcpConns) {
      if (!c.nodeletId) continue;
      if (!byNodelet[c.nodeletId]) byNodelet[c.nodeletId] = [];
      byNodelet[c.nodeletId].push(c);
    }
    return byNodelet;
  }, [mcpConns]);

  const mcpByContainerKey = React.useMemo(() => {
    const byKey: Record<string, ProjectMCPConnection> = {};
    for (const c of mcpConns) {
      if (c.nodeletId && c.containerId) {
        byKey[`${c.nodeletId}:${c.containerId}`] = c;
      }
    }
    return byKey;
  }, [mcpConns]);

// ---- 渲染单个 MCP 连接条目 ----
function MCPTreeItem({
  conn,
  boundContainerId,
  onClick,
}: {
  conn: ProjectMCPConnection;
  boundContainerId?: string;
  onClick?: () => void;
}) {
  const boundName = boundContainerId ? boundContainerId.substring(0, 12) : "";
  return (
    <div
      className="tree-mcp-item"
      role={onClick ? "button" : undefined}
      tabIndex={onClick ? 0 : undefined}
      onClick={onClick}
      onKeyDown={
        onClick
          ? (e: React.KeyboardEvent) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                onClick();
              }
            }
          : undefined
      }
    >
      <Wrench size={12} />
      <div className="tree-container-info">
        <span className="tree-container-name">{conn.name}</span>
        <span className="tree-container-sub">
          {serviceLabel(conn.type) || conn.type}
          {boundContainerId ? ` · ${boundName}…` : ""}
          {" · "}{mcpStatusLabel[conn.status || "stopped"]}
          {conn.toolCount > 0 ? ` · ${conn.toolCount} tools` : ""}
        </span>
      </div>
      <div className="tree-container-side">
        <StatusDot alive={conn.status === "running"} unknown={conn.status !== "running" && conn.status !== "stopped"} />
      </div>
    </div>
  );
}
  // ---- 容器列表子组件（内部调用 TanStack Query） ----
  function ServerContainers({
    nodeletId,
    serverError: srvError,
    onSelectContainer: onSelect,
  }: {
    nodeletId: string;
    serverError?: string;
    onSelectContainer: (id: string) => void;
  }) {
    const { data: conts = [], isLoading, error: containerError } = useContainers(projectId, nodeletId);
    const [expandedHidden, setExpandedHidden] = React.useState(false);
    const onSelectRef = React.useRef(onSelect);
    onSelectRef.current = onSelect;

    // 数据首次加载完成后自动选中第一个可见容器
    React.useEffect(() => {
      if (!isLoading && conts.length > 0 && !selectedContainerID) {
        const excludedRefs = new Set(excludedContainerRefs || []);
        const visible = conts.filter((c) => !excludedRefs.has(`${nodeletId}/${c.id}`));
        if (visible.length > 0) {
          onSelectRef.current(visible[0].id);
        }
      }
      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [isLoading, conts.length, selectedContainerID, excludedContainerRefs, nodeletId]);

    const excludedRefs = new Set(excludedContainerRefs || []);
    const visibleContainers = conts.filter((c) => !excludedRefs.has(`${nodeletId}/${c.id}`));
    const hiddenContainers = conts.filter((c) => excludedRefs.has(`${nodeletId}/${c.id}`));

    return (
      <div className="tree-node-detail">
        {srvError && <div className="tree-node-error">{srvError}</div>}
        {containerError && <div className="tree-node-error">{getErrorMessage(containerError, "读取容器失败")}</div>}
        {isLoading && conts.length === 0 && (
          <div className="loading-overlay"><span className="spinner spinner-sm" /> 加载中...</div>
        )}
        {!isLoading && conts.length === 0 && !srvError && !containerError && (
          <div className="tree-empty">暂无容器</div>
        )}
        {visibleContainers.map((c) => {
          const Icon = serviceTypeIcons[c.serviceType] || serviceTypeIcons.unknown;
          const label = serviceLabel(c.serviceType);
          const portsText = c.ports && c.ports.length > 0
            ? c.ports.map((p) => p.hostPort ? `${p.hostPort}->${p.containerPort}/${p.protocol || "tcp"}` : `${p.containerPort}/${p.protocol || "tcp"}`).join(", ")
            : "";
          const boundMCP = mcpByContainerKey[`${nodeletId}:${c.id}`];
          return (
            <div
              key={c.id}
              className={`tree-container ${selectedContainerID === c.id ? "selected" : ""}`}
              role="button"
              tabIndex={0}
              onClick={() => onSelect(c.id)}
              onKeyDown={(e) => {
                if (e.key === "Enter" || e.key === " ") {
                  e.preventDefault();
                  onSelect(c.id);
                }
              }}
            >
              <Icon size={14} />
              <div className="tree-container-info">
                <span className="tree-container-name">{c.name}</span>
                {(c.status || portsText) && (
                  <span className="tree-container-sub">
                    {[c.status, portsText].filter(Boolean).join("  ·  ")}
                  </span>
                )}
              </div>
              <div className="tree-container-side">
                <Button
                  size="xs" variant="ghost" className="tree-hide-btn" title="隐藏此容器"
                  onClick={(e) => { e.stopPropagation(); excludeContainer.mutate({ nodeletId, containerId: c.id }); }}
                >
                  <EyeOff size={12} />
                </Button>
                {label && <span className="tree-container-type">{label}</span>}
                {boundMCP && (
                  <span className="tree-container-mcp" title={`MCP: ${boundMCP.name} · ${mcpStatusLabel[boundMCP.status || "stopped"]} · ${boundMCP.toolCount} tools`}>
                    <Wrench size={11} />
                    <StatusDot alive={boundMCP.status === "running"} loading={false} unknown={boundMCP.status !== "running" && boundMCP.status !== "stopped"} />
                  </span>
                )}
                <StatusDot alive={c.state === "running"} />
              </div>
            </div>
          );
        })}
        {hiddenContainers.length > 0 && (
          <div className="tree-hidden-section">
            <button
              className="tree-hidden-toggle"
              onClick={() => setExpandedHidden((p) => !p)}
            >
              {expandedHidden ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
              <span>已隐藏 ({hiddenContainers.length})</span>
            </button>
            {expandedHidden &&
              hiddenContainers.map((c) => {
                const Icon = serviceTypeIcons[c.serviceType] || serviceTypeIcons.unknown;
                const label = serviceLabel(c.serviceType);
                return (
                  <div key={c.id} className="tree-hidden-item">
                    <Icon size={14} />
                    <span className="tree-hidden-name">{c.name}</span>
                    <div className="tree-container-side">
                      {label && <span className="tree-container-type">{label}</span>}
                      <button
                        className="tree-restore-btn"
                        title="恢复显示"
                        onClick={() => includeContainer.mutate({ nodeletId, containerId: c.id })}
                      >
                        <Eye size={12} />
                      </button>
                    </div>
                  </div>
                );
              })
            }
          </div>
        )}
      </div>
    );
  }

  function openAddModal() {
    setShowAddModal(true);
  }

  function closeAddModal() {
    setShowAddModal(false);
  }

  function removeServer(nodeletID: string) {
    if (!window.confirm(`确定要从项目中移除服务器吗？`)) return;
    deleteServer.mutate(nodeletID);
  }

  function handleAddServer(nodeletID: string) {
    addServer.mutate(nodeletID, {
      onSuccess: () => setShowAddModal(false),
    });
  }

  const addError = deleteServer.error || addServer.error
    ? getErrorMessage(deleteServer.error || addServer.error, "操作失败")
    : "";
  const addingID = addServer.isPending ? addServer.variables : "";

  const existingIDs = new Set(servers.map((s) => s.nodelet.id));
  const availableNodelets = nodelets.filter((n) => !existingIDs.has(n.id));

  // 有多少 MCP 连接没有绑定到具体容器（仅 nodelet 级别）。
  const nodeletOnlyMCPCount = mcpConns.filter((c) => c.scope === "nodelet").length;
  const containerMCPCount = mcpConns.filter((c) => c.scope === "container").length;

  return (
    <aside className="server-tree">
      <div className="tree-header">
        <Server size={16} />
        <span>服务器</span>
        <Button variant="ghost" size="sm" className="tree-add-btn" title="添加服务器" aria-label="添加服务器" onClick={openAddModal}>
          <Plus size={14} />
        </Button>
      </div>

      {serverError && <div className="error-banner">{serverError}</div>}
      {addError && <div className="error-banner">{addError}</div>}

      <div className="tree-list">
        {serversLoading && servers.length === 0 && (
          <div className="skeleton-block">
            <div className="skeleton-line lg" />
            <div className="skeleton-line md" />
            <div className="skeleton-line md" />
          </div>
        )}
        {!serversLoading && servers.length === 0 && <div className="tree-empty">暂无服务器</div>}
        {servers.length > 0 &&
          servers.map((sw) => {
            const isExpanded = expandedServers.has(sw.nodelet.id);
            const isStatusUnknown = !sw.host?.available && !sw.error;
            const nid = sw.nodelet.id;
            const nodeletMCPConns = mcpByNodelet[nid] || [];
            // Separate MCP connections that are container-bound vs nodelet-only for this server.
            const containerMCPs = nodeletMCPConns.filter((c) => c.scope === "container");
            const nodeletOnlyMCPs = nodeletMCPConns.filter((c) => c.scope === "nodelet");
            return (
              <div key={sw.nodelet.id} className="tree-node">
                <div className="tree-node-main">
                  <button
                    className={`tree-server ${sw.host?.available ? "alive" : "dead"}`}
                    onClick={() => onToggleServer(sw.nodelet.id)}
                  >
                    {isExpanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                    <Server size={15} />
                    <div className="tree-server-info">
                      <strong>{sw.nodelet.name}</strong>
                      <small>{sw.nodelet.address}</small>
                    </div>
                    <StatusDot alive={sw.host?.available ?? false} unknown={isStatusUnknown} />
                  </button>
                  <Button size="xs" variant="ghost" className="tree-remove-btn" title="从项目中移除" onClick={() => removeServer(sw.nodelet.id)}>
                    <Trash2 size={12} />
                  </Button>
                </div>

                {isExpanded && (
                  <ServerContainers
                    nodeletId={sw.nodelet.id}
                    serverError={sw.error}
                    onSelectContainer={(cid) => onSelectContainer(sw.nodelet.id, cid)}
                  />
                )}

                {/* MCP 连接 sub-section: shown when server is expanded and has MCP */}
                {isExpanded && nodeletMCPConns.length > 0 && (
                  <div className="tree-node-detail">
                    <div className="tree-mcp-group-header">
                      <Wrench size={12} />
                      <span>MCP 连接</span>
                      <span className="tree-mcp-group-count">{nodeletMCPConns.length}</span>
                    </div>
                    {/* container-bound MCP rows */}
                    {containerMCPs.map((conn) => (
                      <MCPTreeItem
                        key={conn.id}
                        conn={conn}
                        boundContainerId={conn.containerId}
                        onClick={
                          conn.containerId
                            ? () => onSelectContainer(nid, conn.containerId!)
                            : undefined
                        }
                      />
                    ))}
                    {/* nodelet-only MCP rows */}
                    {nodeletOnlyMCPs.map((conn) => (
                      <MCPTreeItem key={conn.id} conn={conn} />
                    ))}
                  </div>
                )}
              </div>
            );
          })}

        {/* Global MCP section: only shown when there are connections */}
        {!mcpLoading && mcpConns.length > 0 && (
          <div className="tree-mcp-summary">
            <Wrench size={13} />
            <span>
              {nodeletOnlyMCPCount > 0 && containerMCPCount > 0
                ? `${mcpConns.length} 个 MCP（${containerMCPCount} 绑定容器 + ${nodeletOnlyMCPCount} 服务器级）`
                : `${mcpConns.length} 个 MCP 连接`
              }
            </span>
          </div>
        )}
        {!mcpLoading && mcpConns.length === 0 && !serversLoading && servers.length > 0 && (
          <div className="tree-mcp-summary tree-mcp-summary--empty">
            <Wrench size={13} />
            <span>暂无项目 MCP 连接</span>
          </div>
        )}
        {mcpLoading && (
          <div className="loading-overlay"><span className="spinner spinner-sm" /> 读取 MCP 连接中...</div>
        )}
        {mcpError && <div className="tree-node-error">{mcpError}</div>}
      </div>

      {showAddModal && (
        <Modal title="添加服务器" onClose={closeAddModal}>
          {addError && <div className="error-banner">{addError}</div>}
          {nodeletsLoading ? (
            <div className="tree-empty">读取可用服务器中...</div>
          ) : availableNodelets.length === 0 ? (
            <div className="tree-empty">{nodelets.length === 0 ? "没有可用的服务器，请先在配置中添加 nodelet" : "所有服务器已添加到项目中"}</div>
          ) : (
            <ul className="nodelet-pick-list">
              {availableNodelets.map((n) => (
                <li key={n.id}>
                  <div className="nodelet-pick-info">
                    <strong>{n.name}</strong>
                    <small>{n.address}</small>
                  </div>
                  <Button
                    size="sm"
                    disabled={addingID === n.id}
                    onClick={() => handleAddServer(n.id)}
                  >
                    {addingID === n.id ? "添加中..." : "添加"}
                  </Button>
                </li>
              ))}
            </ul>
          )}
        </Modal>
      )}
    </aside>
  );
}
