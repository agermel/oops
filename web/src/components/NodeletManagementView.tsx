import React from "react";
import { Plus, Trash2, Edit3, Server, RefreshCw } from "lucide-react";
import type { NodeletConfig, NodeletStatusItem, ProbeStatus } from "../types";
import { apiRequest, getErrorMessage } from "../lib/api";
import { NodeletFormModal } from "./NodeletFormModal";
import { Button } from "./ui/Button";
import { StatusDot } from "./StatusPill";

type NodeletRow = NodeletConfig & {
  status?: ProbeStatus;
  statusError?: string;
  latencyMs?: number;
};

// ---- NodeletManagementView ----
export function NodeletManagementView() {
  const [nodelets, setNodelets] = React.useState<NodeletRow[]>([]);
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState("");

  const [showForm, setShowForm] = React.useState(false);
  const [editItem, setEditItem] = React.useState<NodeletConfig | null>(null);

  async function fetchStatus() {
    try {
      const items = await apiRequest<NodeletStatusItem[]>("/api/nodelets/status");
      setNodelets((prev) => {
        const statusMap = new Map(items.map((s) => [s.nodelet.id, s]));
        // 如果当前列表为空（首次加载），从 status 响应创建
        if (prev.length === 0) {
          return items.map((s) => ({
            ...s.nodelet,
            status: s.status,
            statusError: s.error,
            latencyMs: s.latencyMs,
          }));
        }
        // 增量更新状态
        return prev.map((n) => {
          const si = statusMap.get(n.id);
          if (!si) return n;
          return { ...n, status: si.status, statusError: si.error, latencyMs: si.latencyMs };
        });
      });
    } catch (err) {
      // 静默处理轮询错误
    }
  }

  async function fetchNodelets() {
    setLoading(true);
    setError("");
    try {
      const configs = await apiRequest<NodeletConfig[]>("/api/nodelets");
      const statusItems = await apiRequest<NodeletStatusItem[]>("/api/nodelets/status");
      const statusMap = new Map(statusItems.map((s) => [s.nodelet.id, s]));
      setNodelets(
        configs.map((c) => {
          const si = statusMap.get(c.id);
          return {
            ...c,
            status: si?.status ?? ("unknown" as ProbeStatus),
            statusError: si?.error,
            latencyMs: si?.latencyMs,
          };
        }),
      );
    } catch (err) {
      setError(getErrorMessage(err, "读取服务器列表失败"));
    } finally {
      setLoading(false);
    }
  }

  // 15s 轮询读取 Prober 缓存（极快，无阻塞）
  React.useEffect(() => {
    fetchNodelets();
    const timer = setInterval(fetchStatus, 15_000);
    return () => clearInterval(timer);
  }, []);

  async function handleRefresh() {
    setError("");
    try {
      await apiRequest("/api/nodelets/probe-all", { method: "POST" });
      await fetchStatus();
    } catch (err) {
      setError(getErrorMessage(err, "刷新状态失败"));
    }
  }

  async function handleRetrySingle(id: string) {
    try {
      await apiRequest(`/api/nodelets/${encodeURIComponent(id)}/probe`, { method: "POST" });
      await fetchStatus();
    } catch (err) {
      setError(getErrorMessage(err, "探测失败"));
    }
  }

  function openAdd() {
    setEditItem(null);
    setShowForm(true);
  }

  function openEdit(item: NodeletRow) {
    setEditItem(item);
    setShowForm(true);
  }

  async function handleDelete(id: string, name: string) {
    if (!window.confirm(`确定要删除服务器 "${name}" 吗？`)) return;
    try {
      await apiRequest(`/api/nodelets/${encodeURIComponent(id)}`, { method: "DELETE" });
      fetchNodelets();
    } catch (err) {
      setError(getErrorMessage(err, "删除失败"));
    }
  }

  function onFormSaved() {
    setShowForm(false);
    setEditItem(null);
    fetchNodelets();
  }

  function statusDotProps(status: ProbeStatus | undefined) {
    switch (status) {
      case "healthy":
        return { alive: true };
      case "dead":
        return { alive: false };
      case "unhealthy":
        return { alive: false, unknown: true };
      case "probing":
        return { alive: false, loading: true };
      default:
        return { alive: false, unknown: true };
    }
  }

  return (
    <section className="panel">
      <div className="panel-summary">
        <div className="section-header-spread">
          <h2 className="section-header">
            <Server size={18} />
            <span>服务器</span>
          </h2>
          <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
            <span style={{ color: "var(--muted)", fontSize: 13 }}>
              {nodelets.length} 台
            </span>
            <Button size="sm" onClick={openAdd}>
              <Plus size={14} /> 添加
            </Button>
            <Button size="sm" variant="ghost" onClick={handleRefresh} disabled={loading}>
              刷新
            </Button>
          </div>
        </div>
      </div>

      {error && <div className="error-banner">{error}</div>}

      {loading && nodelets.length === 0 ? (
        <div className="skeleton-block" style={{ height: 120 }} />
      ) : nodelets.length === 0 ? (
        <div className="empty-state">
          <p>暂无服务器</p>
          <p style={{ color: "var(--muted)", marginTop: 4 }}>
            添加 Nodelet 服务器以开始监控容器。
          </p>
        </div>
      ) : (
        <div className="mcp-table-wrap nodelet-table-wrap">
          <table>
            <colgroup>
              <col className="nodelet-col-name" />
              <col className="nodelet-col-address" />
              <col className="nodelet-col-status" />
              <col className="nodelet-col-actions" />
            </colgroup>
            <thead>
              <tr>
                <th>名称</th>
                <th>地址</th>
                <th>状态</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {nodelets.map((n) => (
                <tr key={n.id}>
                  <td>
                    <span style={{ fontWeight: 500 }}>{n.name}</span>
                  </td>
                  <td className="mono nodelet-address-cell">
                    {n.address}
                  </td>
                  <td className="nodelet-status-cell">
                    <StatusDot
                      {...statusDotProps(n.status)}
                      title={
                        n.statusError
                          ? n.statusError
                          : n.status === "healthy" && n.latencyMs
                            ? `${n.latencyMs}ms`
                            : undefined
                      }
                    />
                    {n.statusError && <span className="error-hint">{n.statusError}</span>}
                  </td>
                  <td className="nodelet-actions-cell">
                    <div className="nodelet-actions">
                      <Button
                        size="xs"
                        variant="ghost"
                        iconOnly
                        onClick={() => handleRetrySingle(n.id)}
                        title="重新探测"
                        aria-label={`重新探测 ${n.name}`}
                      >
                        <RefreshCw size={12} />
                      </Button>
                      <Button size="xs" variant="ghost" iconOnly onClick={() => openEdit(n)} aria-label={`编辑 ${n.name}`}>
                        <Edit3 size={12} />
                      </Button>
                      <Button size="xs" variant="ghost" iconOnly onClick={() => handleDelete(n.id, n.name)} aria-label={`删除 ${n.name}`}>
                        <Trash2 size={12} />
                      </Button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {showForm && (
        <NodeletFormModal
          editItem={editItem}
          onSaved={onFormSaved}
          onClose={() => {
            setShowForm(false);
            setEditItem(null);
          }}
        />
      )}
    </section>
  );
}
