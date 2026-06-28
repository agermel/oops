import { Server, ChevronDown, ChevronRight, Plus, X } from "lucide-react";
import React from "react";
import type { ServerWithNodelet, ContainerWithType, NodeletItem } from "../types";
import { serviceTypeIcons, serviceTypeLabels } from "../types";

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
  containersLoading: boolean;
  selectedContainerID: string;
  expandedServers: Set<string>;
  onToggleServer: (nodeletID: string) => void;
  onSelectContainer: (nodeletID: string, containerID: string) => void;
  onServersChanged: () => void;
}) {
  const [showAddModal, setShowAddModal] = React.useState(false);
  const [nodelets, setNodelets] = React.useState<NodeletItem[]>([]);
  const [nodeletsLoading, setNodeletsLoading] = React.useState(false);
  const [addError, setAddError] = React.useState("");
  const [addingID, setAddingID] = React.useState("");

  async function openAddModal() {
    setShowAddModal(true);
    setAddError("");
    setNodeletsLoading(true);
    try {
      const resp = await fetch("/api/nodelets");
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      setNodelets(await resp.json());
    } catch (err) {
      setAddError(err instanceof Error ? err.message : "读取服务器列表失败");
    } finally {
      setNodeletsLoading(false);
    }
  }

  async function addServer(nodeletID: string) {
    setAddingID(nodeletID);
    setAddError("");
    try {
      const resp = await fetch(`/api/projects/${encodeURIComponent(projectId)}/servers`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ nodeletId: nodeletID }),
      });
      if (!resp.ok) {
        const data = await resp.json().catch(() => ({ error: `HTTP ${resp.status}` }));
        throw new Error(data.error || `HTTP ${resp.status}`);
      }
      setShowAddModal(false);
      onServersChanged();
    } catch (err) {
      setAddError(err instanceof Error ? err.message : "添加失败");
    } finally {
      setAddingID("");
    }
  }

  const existingIDs = new Set(servers.map((s) => s.nodelet.id));
  const availableNodelets = nodelets.filter((n) => !existingIDs.has(n.nodelet.id));
  return (
    <aside className="server-tree">
      <div className="tree-header">
        <Server size={16} />
        <span>服务器</span>
        <button className="ghost-button small tree-add-btn" title="添加服务器" onClick={openAddModal}>
          <Plus size={14} />
        </button>
      </div>

      {serverError && <div className="error-line">{serverError}</div>}

      <div className="tree-list">
        {serversLoading && servers.length === 0 ? (
          <div className="tree-empty">读取中...</div>
        ) : servers.length === 0 ? (
          <div className="tree-empty">暂无服务器</div>
        ) : (
          servers.map((sw) => {
            const isExpanded = expandedServers.has(sw.nodelet.id);
            const conts = containers[sw.nodelet.id] || [];
            return (
              <div key={sw.nodelet.id} className="tree-node">
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
                  <span className={`status-dot ${sw.host?.available ? "alive" : "dead"}`} />
                </button>

                {isExpanded && (
                  <div className="tree-containers">
                    {containersLoading && conts.length === 0 ? (
                      <div className="tree-empty">读取容器中...</div>
                    ) : conts.length === 0 ? (
                      <div className="tree-empty">暂无容器</div>
                    ) : (
                      conts.map((c) => {
                        const Icon = serviceTypeIcons[c.serviceType] || serviceTypeIcons.unknown;
                        return (
                          <button
                            key={c.id}
                            className={`tree-container ${selectedContainerID === c.id ? "selected" : ""}`}
                            onClick={() => onSelectContainer(sw.nodelet.id, c.id)}
                          >
                            <Icon size={14} />
                            <span className="tree-container-name">{c.name}</span>
                            <span className="tree-container-type">{serviceTypeLabels[c.serviceType] || c.serviceType}</span>
                            <span className={`status-dot ${c.state === "running" ? "alive" : "dead"}`} />
                          </button>
                        );
                      })
                    )}
                  </div>
                )}
              </div>
            );
          })
        )}
      </div>

      {/* 添加服务器弹窗 */}
      {showAddModal && (
        <div className="modal-overlay" onClick={() => setShowAddModal(false)}>
          <div className="modal-card" onClick={(e) => e.stopPropagation()}>
            <div className="modal-head">
              <h2>添加服务器</h2>
              <button className="ghost-button" onClick={() => setShowAddModal(false)}><X size={18} /></button>
            </div>
            <div className="modal-body">
              {addError && <div className="error-line">{addError}</div>}
              {nodeletsLoading ? (
                <div className="tree-empty">读取可用服务器中...</div>
              ) : availableNodelets.length === 0 ? (
                <div className="tree-empty">{nodelets.length === 0 ? "没有可用的服务器，请先在配置中添加 nodelet" : "所有服务器已添加到项目中"}</div>
              ) : (
                <ul className="nodelet-pick-list">
                  {availableNodelets.map((n) => (
                    <li key={n.nodelet.id}>
                      <div className="nodelet-pick-info">
                        <strong>{n.nodelet.name || n.nodelet.id}</strong>
                        <small>{n.nodelet.address}</small>
                        {n.available !== undefined && (
                          <span className={`status-dot ${n.available ? "alive" : "dead"}`} />
                        )}
                      </div>
                      <button
                        className="primary-button small"
                        disabled={addingID === n.nodelet.id}
                        onClick={() => addServer(n.nodelet.id)}
                      >
                        {addingID === n.nodelet.id ? "添加中..." : "添加"}
                      </button>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          </div>
        </div>
      )}
    </aside>
  );
}
