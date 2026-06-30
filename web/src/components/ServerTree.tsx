import { Server, ChevronDown, ChevronRight, Plus, Trash2 } from "lucide-react";
import React from "react";
import type { ServerWithNodelet, ContainerWithType, NodeletConfig } from "../types";
import { serviceTypeIcons, serviceLabel } from "../types";
import { apiRequest, getErrorMessage } from "../lib/api";
import { projectPaths } from "../lib/paths";
import { Modal } from "./Modal";
import { StatusDot } from "./StatusPill";
import { Button } from "./ui/Button";

export function ServerTree({
  projectId,
  servers,
  serversLoading,
  serverError,
  containers,
  containersLoading,
  selectedContainerID,
  expandedServers,
  onToggleServer,
  onSelectContainer,
  onServersChanged,
}: {
  projectId: string;
  servers: ServerWithNodelet[];
  serversLoading: boolean;
  serverError: string;
  containers: Record<string, ContainerWithType[]>;
  containersLoading: Set<string>;
  selectedContainerID: string;
  expandedServers: Set<string>;
  onToggleServer: (nodeletID: string) => void;
  onSelectContainer: (nodeletID: string, containerID: string) => void;
  onServersChanged: () => void;
}) {
  const [showAddModal, setShowAddModal] = React.useState(false);
  const [nodelets, setNodelets] = React.useState<NodeletConfig[]>([]);
  const [nodeletsLoading, setNodeletsLoading] = React.useState(false);
  const [addError, setAddError] = React.useState("");
  const [addingID, setAddingID] = React.useState("");
  const [removeError, setRemoveError] = React.useState("");

  const paths = projectPaths(projectId);

  async function openAddModal() {
    setShowAddModal(true);
    setAddError("");
    setNodeletsLoading(true);
    try {
      setNodelets(await apiRequest<NodeletConfig[]>("/api/nodelets"));
    } catch (err) {
      setAddError(getErrorMessage(err, "读取服务器列表失败"));
    } finally {
      setNodeletsLoading(false);
    }
  }

  function closeAddModal() {
    setShowAddModal(false);
  }

  async function removeServer(nodeletID: string) {
    if (!window.confirm(`确定要从项目中移除服务器吗？`)) return;
    setRemoveError("");
    try {
      await apiRequest(`${paths.servers}/${encodeURIComponent(nodeletID)}`, { method: "DELETE" });
      onServersChanged();
    } catch (err) {
      setRemoveError(getErrorMessage(err, "移除失败"));
    }
  }

  async function addServer(nodeletID: string) {
    setAddingID(nodeletID);
    setAddError("");
    try {
      await apiRequest(paths.servers, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ nodeletId: nodeletID }),
      });
      setShowAddModal(false);
      onServersChanged();
    } catch (err) {
      setAddError(getErrorMessage(err, "添加失败"));
    } finally {
      setAddingID("");
    }
  }

  const existingIDs = new Set(servers.map((s) => s.nodelet.id));
  const availableNodelets = nodelets.filter((n) => !existingIDs.has(n.id));
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
      {removeError && <div className="error-banner">{removeError}</div>}

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
            const conts = containers[sw.nodelet.id] || [];
            const isContainerLoading = containersLoading.has(sw.nodelet.id);
            const hasContainerResult = Object.prototype.hasOwnProperty.call(containers, sw.nodelet.id);
            const isStatusUnknown = !sw.host?.available && !sw.error && !hasContainerResult;
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
                      <strong>{sw.nodelet.name || sw.nodelet.id}</strong>
                      <small>{sw.nodelet.address}</small>
                    </div>
                    <StatusDot alive={sw.host?.available ?? false} loading={isContainerLoading} unknown={isStatusUnknown} />
                  </button>
                  <Button size="xs" variant="ghost" className="tree-remove-btn" title="从项目中移除" onClick={() => removeServer(sw.nodelet.id)}>
                    <Trash2 size={12} />
                  </Button>
                </div>

                {isExpanded && (
                  <div className="tree-node-detail">
                    {sw.error && <div className="tree-node-error">{sw.error}</div>}
                    {isContainerLoading && conts.length === 0 && (
                      <div className="loading-overlay"><span className="spinner spinner-sm" /> 加载中...</div>
                    )}
                    {!isContainerLoading && conts.length === 0 && !sw.error ? (
                      <div className="tree-empty">暂无容器</div>
                    ) : (
                      conts.map((c) => {
                        const Icon = serviceTypeIcons[c.serviceType] || serviceTypeIcons.unknown;
                        const label = serviceLabel(c.serviceType);
                        return (
                          <button
                            key={c.id}
                            className={`tree-container ${selectedContainerID === c.id ? "selected" : ""}`}
                            onClick={() => onSelectContainer(sw.nodelet.id, c.id)}
                          >
                            <Icon size={14} />
                            <span className="tree-container-name">{c.name}</span>
                            {label && <span className="tree-container-type">{label}</span>}
                            <StatusDot alive={c.state === "running"} />
                          </button>
                        );
                      })
                    )}
                  </div>
                )}
              </div>
            );
          })}
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
                    <strong>{n.name || n.id}</strong>
                    <small>{n.address}</small>
                  </div>
                  <Button
                    size="sm"
                    disabled={addingID === n.id}
                    onClick={() => addServer(n.id)}
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
