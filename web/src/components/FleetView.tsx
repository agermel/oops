import { Server, TerminalSquare, FileText } from "lucide-react";
import type { NodeletItem, Container, LogEntry } from "../types";
import { LogRow } from "./LogRow";

export function FleetView({
  nodelets,
  nodeletsLoading,
  nodeletError,
  selectedNodelet,
  containers,
  containersLoading,
  logs,
  logsLoading,
  selectedContainer,
  autoScroll,
  onSelectNodelet,
  onLoadLogs,
  onClearLogs,
  onAutoScrollChange,
  logsPanelRef,
}: {
  nodelets: NodeletItem[];
  nodeletsLoading: boolean;
  nodeletError: string;
  selectedNodelet: string;
  containers: Container[];
  containersLoading: boolean;
  logs: LogEntry[];
  logsLoading: boolean;
  selectedContainer: string;
  autoScroll: boolean;
  onSelectNodelet: (id: string) => void;
  onLoadLogs: (nodeletId: string, containerId: string) => void;
  onClearLogs: () => void;
  onAutoScrollChange: (checked: boolean) => void;
  logsPanelRef: React.RefObject<HTMLDivElement | null>;
}) {
  return (
    <>
      <section className="fleet-section">
        <div className="section-title">
          <Server size={18} />
          <h2>机器列表</h2>
        </div>
        {nodeletError && <div className="error-line">{nodeletError}</div>}
        <div className="nodelet-grid">
          {nodeletsLoading && nodelets.length === 0 ? (
            <div className="empty-card">正在读取机器列表</div>
          ) : (
            nodelets.map((item) => (
              <button
                key={item.nodelet.id}
                className={`nodelet-card ${selectedNodelet === item.nodelet.id ? "selected" : ""}`}
                onClick={() => onSelectNodelet(item.nodelet.id)}
              >
                <span className={`nodelet-dot ${item.available ? "alive" : "dead"}`} />
                <span>
                  <strong>{item.host.name || item.nodelet.name || item.nodelet.id}</strong>
                  <small>{item.nodelet.address}</small>
                </span>
                <span className="nodelet-meta">
                  {item.available ? `${item.host.runtime || "docker"} ${item.host.dockerVersion || ""}` : "unavailable"}
                </span>
              </button>
            ))
          )}
        </div>
      </section>

      <section className="fleet-section">
        <div className="section-title">
          <TerminalSquare size={18} />
          <h2>容器列表</h2>
        </div>
        <div className="table-wrap compact">
          <table>
            <thead>
              <tr>
                <th>容器</th>
                <th>镜像</th>
                <th>状态</th>
                <th>健康</th>
                <th>主机</th>
                <th>日志</th>
              </tr>
            </thead>
            <tbody>
              {containersLoading ? (
                <tr>
                  <td colSpan={6} className="empty">
                    正在读取容器列表
                  </td>
                </tr>
              ) : containers.length === 0 ? (
                <tr>
                  <td colSpan={6} className="empty">
                    暂无容器
                  </td>
                </tr>
              ) : (
                containers.map((container) => (
                  <tr key={container.id} className={selectedContainer === container.id ? "selected-row" : ""}>
                    <td>
                      <div className="service-name">{container.name || container.id.slice(0, 12)}</div>
                      <div className="service-id">{container.id.slice(0, 12)}</div>
                    </td>
                    <td className="address">{container.image}</td>
                    <td>
                      <span className={`status ${container.state === "running" ? "alive" : "unknown"}`}>
                        {container.state}
                      </span>
                    </td>
                    <td>{container.health || "-"}</td>
                    <td className="message">{container.hostId}</td>
                    <td className="action-cell">
                      <button title="查看日志" onClick={() => onLoadLogs(selectedNodelet, container.id)}>
                        <FileText size={18} />
                      </button>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </section>

      <section className="fleet-section">
        <div className="section-title logs-title">
          <span>
            <FileText size={18} />
            <h2>日志列表</h2>
          </span>
          <div className="log-actions">
            <label>
              <input type="checkbox" checked={autoScroll} onChange={(event) => onAutoScrollChange(event.target.checked)} />
              <span>自动滚动</span>
            </label>
            <button
              type="button"
              onClick={onClearLogs}
              disabled={logs.length === 0}
            >
              清空
            </button>
          </div>
        </div>
        <div className="logs-panel" ref={logsPanelRef}>
          {logsLoading ? (
            <div className="empty-card">正在连接日志流</div>
          ) : logs.length === 0 ? (
            <div className="empty-card">{selectedContainer ? "等待实时日志" : "选择一个容器查看最近 100 行日志"}</div>
          ) : (
            logs.map((entry, index) => <LogRow key={`${entry.timestamp}-${index}`} entry={entry} />)
          )}
        </div>
      </section>
    </>
  );
}
