import React from "react";
import { Plus, Trash2, Edit3, Server, RefreshCw } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import type { NodeletConfig, NodeletStatusItem, ProbeStatus } from "../types";
import { queryKeys } from "../hooks/queries";
import { apiRequest, getErrorMessage } from "../lib/api";
import { nodeletPaths, nodeletBasePaths } from "../lib/paths";
import { useNodelets } from "../hooks/useNodelets";
import { useNodeletStatus } from "../hooks/useServers";
import { useModal } from "../hooks/useModal";
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
  const { data: configs = [], isLoading: configsLoading, error: configsError } = useNodelets();
  const { data: statusItems = [] } = useNodeletStatus();
  const [error, setError] = React.useState("");

  const formModal = useModal<NodeletConfig>();

  // 合并 configs + prober status
  const nodelets = React.useMemo<NodeletRow[]>(() => {
    const statusMap = new Map(statusItems.map((s: NodeletStatusItem) => [s.nodelet.id, s]));
    return configs.map((c: NodeletConfig) => {
      const si = statusMap.get(c.id);
      return {
        ...c,
        status: si?.status ?? ("unknown" as ProbeStatus),
        statusError: si?.error,
        latencyMs: si?.latencyMs,
      };
    });
  }, [configs, statusItems]);

  const loading = configsLoading && nodelets.length === 0;
  const queryError = configsError ? getErrorMessage(configsError, "读取服务器列表失败") : "";
  const queryClient = useQueryClient();

  async function handleRefresh() {
    setError("");
    try {
      await apiRequest(nodeletBasePaths.probeAll, { method: "POST" });
      queryClient.invalidateQueries({ queryKey: queryKeys.nodelets.status });
    } catch (err) {
      setError(getErrorMessage(err, "刷新状态失败"));
    }
  }

  async function handleRetrySingle(id: string) {
    try {
      await apiRequest(nodeletPaths(id).probe, { method: "POST" });
      queryClient.invalidateQueries({ queryKey: queryKeys.nodelets.status });
    } catch (err) {
      setError(getErrorMessage(err, "探测失败"));
    }
  }

  async function handleDelete(id: string, name: string) {
    if (!window.confirm(`确定要删除服务器 "${name}" 吗？`)) return;
    try {
      await apiRequest(nodeletPaths(id).detail, { method: "DELETE" });
      queryClient.invalidateQueries({ queryKey: queryKeys.nodelets.all });
    } catch (err) {
      setError(getErrorMessage(err, "删除失败"));
    }
  }

  function onFormSaved() {
    formModal.onClose();
    queryClient.invalidateQueries({ queryKey: queryKeys.nodelets.all });
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
            <Button size="sm" onClick={() => formModal.onOpen()}>
              <Plus size={14} /> 添加
            </Button>
            <Button size="sm" variant="ghost" onClick={handleRefresh} disabled={loading}>
              刷新
            </Button>
          </div>
        </div>
      </div>

      {(error || queryError) && <div className="error-banner">{error || queryError}</div>}

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
                      <Button size="xs" variant="ghost" iconOnly onClick={() => formModal.onOpen(n)} aria-label={`编辑 ${n.name}`}>
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

      {formModal.open && (
        <NodeletFormModal
          editItem={formModal.data}
          onSaved={onFormSaved}
          onClose={formModal.onClose}
        />
      )}
    </section>
  );
}
