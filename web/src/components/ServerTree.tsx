import { Server, ChevronDown, ChevronRight, Plus, Trash2, EyeOff, Eye, Wrench } from "lucide-react";
import React from "react";
import type { ServerWithNodelet, ProjectMCPConnection } from "../types";
import { serviceTypeIcons, serviceLabel, mcpStatusLabel } from "../types";
import { getErrorMessage } from "../lib/api";
import { useDeleteServer, useAddServer, useExcludeContainer, useIncludeContainer, useContainers, useProjectMCPConnections } from "../hooks/useServers";
import { useNodelets } from "../hooks/useNodelets";
import { useToggle } from "../hooks/useToggle";
import { Modal } from "./Modal";
import { StatusDot } from "./StatusPill";
import { Button } from "./ui/Button";

type ContainerVisibilityMutation = Pick<ReturnType<typeof useExcludeContainer>, "mutate">;

function ServerContainers({
  projectId,
  nodeletId,
  serverError: srvError,
  selectedNodeletID,
  selectedContainerID,
  excludedContainerRefs,
  onSelectContainer: onSelect,
  excludeContainer,
  includeContainer,
}: {
  projectId: string;
  nodeletId: string;
  serverError?: string;
  selectedNodeletID: string;
  selectedContainerID: string;
  excludedContainerRefs?: string[];
  onSelectContainer: (id: string) => void;
  excludeContainer: ContainerVisibilityMutation;
  includeContainer: ContainerVisibilityMutation;
}) {
  const { data: conts = [], isLoading, error: containerError } = useContainers(projectId, nodeletId);
  const [expandedHidden, setExpandedHidden] = React.useState(false);
  const onSelectRef = React.useRef(onSelect);
  onSelectRef.current = onSelect;

  const excludedRefs = React.useMemo(() => new Set(excludedContainerRefs || []), [excludedContainerRefs]);
  const visibleContainers = React.useMemo(
    () => conts.filter((c) => !excludedRefs.has(`${nodeletId}/${c.id}`)),
    [conts, excludedRefs, nodeletId],
  );
  const hiddenContainers = React.useMemo(
    () => conts.filter((c) => excludedRefs.has(`${nodeletId}/${c.id}`)),
    [conts, excludedRefs, nodeletId],
  );
  const selectedInThisNodelet = selectedNodeletID === nodeletId;
  const selectedHidden = selectedInThisNodelet && hiddenContainers.some((c) => c.id === selectedContainerID);

  React.useEffect(() => {
    if (!isLoading && conts.length > 0 && !selectedContainerID && selectedInThisNodelet) {
      if (visibleContainers.length > 0) {
        onSelectRef.current(visibleContainers[0].id);
      }
    }
  }, [isLoading, conts.length, selectedContainerID, selectedInThisNodelet, visibleContainers]);

  React.useEffect(() => {
    if (selectedHidden) setExpandedHidden(true);
  }, [selectedHidden]);

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
        const selected = selectedInThisNodelet && selectedContainerID === c.id;
        return (
          <div
            key={c.id}
            className={`tree-container ${selected ? "selected" : ""}`}
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
              const selected = selectedInThisNodelet && selectedContainerID === c.id;
              return (
                <div
                  key={c.id}
                  className={`tree-hidden-item ${selected ? "selected" : ""}`}
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
                  <span className="tree-hidden-name">{c.name}</span>
                  <div className="tree-container-side">
                    {label && <span className="tree-container-type">{label}</span>}
                    <button
                      className="tree-restore-btn"
                      title="恢复显示"
                      onClick={(e) => {
                        e.stopPropagation();
                        includeContainer.mutate({ nodeletId, containerId: c.id });
                      }}
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

export function ServerTree({
  projectId,
  servers,
  serversLoading,
  serverError,
  selectedNodeletID,
  selectedContainerID,
  selectedMCPConnectionID,
  expandedServers,
  excludedContainerRefs,
  onToggleServer,
  onSelectContainer,
  onSelectMCPConnection,
}: {
  projectId: string;
  servers: ServerWithNodelet[];
  serversLoading: boolean;
  serverError: string;
  selectedNodeletID: string;
  selectedContainerID: string;
  selectedMCPConnectionID?: string;
  expandedServers: Set<string>;
  excludedContainerRefs?: string[];
  onToggleServer: (nodeletID: string) => void;
  onSelectContainer: (nodeletID: string, containerID: string) => void;
  onSelectMCPConnection: (conn: ProjectMCPConnection) => void;
}) {
  const [showAddModal, setShowAddModal] = React.useState(false);
  const [titleEditing, setTitleEditing] = React.useState(false);
  const [title, setTitle] = React.useState("资源");
  const [titleDraft, setTitleDraft] = React.useState("资源");
  const titleInputRef = React.useRef<HTMLInputElement>(null);

  const [serversExpanded, { toggle: toggleServers }] = useToggle(true);
  const [mcpExpanded, { toggle: toggleMCP }] = useToggle(false);

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

  function commitTitle() {
    setTitle(titleDraft.trim() || title);
    setTitleEditing(false);
  }

  React.useEffect(() => {
    if (titleEditing && titleInputRef.current) {
      titleInputRef.current.focus();
      titleInputRef.current.select();
    }
  }, [titleEditing]);

  const addError = deleteServer.error || addServer.error
    ? getErrorMessage(deleteServer.error || addServer.error, "操作失败")
    : "";
  const addingID = addServer.isPending ? addServer.variables : "";

  const existingIDs = new Set(servers.map((s) => s.nodelet.id));
  const availableNodelets = nodelets.filter((n) => !existingIDs.has(n.id));

  return (
    <aside className="server-tree">
      {/* Header — 可编辑标题 */}
      <div className="tree-header">
        <Server size={16} />
        {titleEditing ? (
          <input
            ref={titleInputRef}
            className="tree-header-input"
            value={titleDraft}
            onChange={(e) => setTitleDraft(e.target.value)}
            onBlur={commitTitle}
            onKeyDown={(e) => {
              if (e.key === "Enter") commitTitle();
              if (e.key === "Escape") { setTitleDraft(title); setTitleEditing(false); }
            }}
          />
        ) : (
          <span
            className="tree-header-title"
            onClick={() => { setTitleDraft(title); setTitleEditing(true); }}
            title="点击编辑标题"
          >
            {title}
          </span>
        )}
        <Button variant="ghost" size="sm" className="tree-add-btn" title="添加服务器" aria-label="添加服务器" onClick={openAddModal}>
          <Plus size={14} />
        </Button>
      </div>

      {serverError && <div className="error-banner">{serverError}</div>}
      {addError && <div className="error-banner">{addError}</div>}

      {/* ---- 统一滚动区：服务器区 + MCP 区 ---- */}
      <div className="tree-list">
        {/* ---- 服务器区（可折叠）---- */}
        <button className="tree-section-toggle" onClick={toggleServers}>
          {serversExpanded ? <ChevronDown size={13} /> : <ChevronRight size={13} />}
          <Server size={13} />
          <span>服务器</span>
          {servers.length > 0 && (
            <span className="tree-section-count">{servers.length}</span>
          )}
        </button>
        {serversExpanded && (
          <div className="tree-section-body">
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
                        projectId={projectId}
                        nodeletId={sw.nodelet.id}
                        serverError={sw.error}
                        selectedNodeletID={selectedNodeletID}
                        selectedContainerID={selectedContainerID}
                        excludedContainerRefs={excludedContainerRefs}
                        onSelectContainer={(cid) => onSelectContainer(sw.nodelet.id, cid)}
                        excludeContainer={excludeContainer}
                        includeContainer={includeContainer}
                      />
                    )}
                  </div>
                );
              })}
          </div>
        )}

        {/* ---- MCP 连接区（可折叠，与服务器同级）---- */}
        <button className="tree-section-toggle" onClick={toggleMCP}>
          {mcpExpanded ? <ChevronDown size={13} /> : <ChevronRight size={13} />}
          <Wrench size={13} />
          <span>MCP 连接</span>
          {mcpConns.length > 0 && (
            <span className="tree-section-count">{mcpConns.length}</span>
          )}
        </button>
        {mcpExpanded && (
          <div className="tree-section-body">
            {mcpError && <div className="tree-node-error">{mcpError}</div>}
            {mcpLoading && mcpConns.length === 0 && (
              <div className="loading-overlay"><span className="spinner spinner-sm" /> 读取中...</div>
            )}
            {!mcpLoading && mcpConns.length === 0 && !mcpError && (
              <div className="tree-empty">暂无 MCP 连接</div>
            )}
            {mcpConns.map((conn) => {
              const Icon = serviceTypeIcons[conn.type] || serviceTypeIcons.unknown;
              const isSelected = selectedMCPConnectionID === conn.id;
              return (
                <div
                  key={conn.id}
                  className={`tree-container tree-mcp-row ${isSelected ? "selected" : ""}`}
                  role="button"
                  tabIndex={0}
                  onClick={() => onSelectMCPConnection(conn)}
                  onKeyDown={(e: React.KeyboardEvent) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault();
                      onSelectMCPConnection(conn);
                    }
                  }}
                >
                  <span className="tree-row-toggle-spacer" aria-hidden="true" />
                  <Icon size={14} />
                  <div className="tree-container-info">
                    <span className="tree-container-name">{conn.name}</span>
                    <span className="tree-container-sub">
                      {mcpStatusLabel[conn.status || "stopped"]}
                      {conn.toolCount > 0 ? ` · ${conn.toolCount} tools` : ""}
                    </span>
                  </div>
                  <div className="tree-container-side">
                    <StatusDot alive={conn.status === "running"} unknown={conn.status !== "running" && conn.status !== "stopped"} />
                  </div>
                  <span className="tree-row-action-spacer" aria-hidden="true" />
                </div>
              );
            })}
          </div>
        )}
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
