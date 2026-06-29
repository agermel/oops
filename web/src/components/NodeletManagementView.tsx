import React from "react";
import { Plus, Trash2, Edit3, Server, Loader2 } from "lucide-react";
import type { NodeletConfig } from "../types";
import { apiRequest, getErrorMessage } from "../lib/api";
import { NodeletFormModal } from "./NodeletFormModal";
import { Button } from "./ui/Button";

type NodeletRow = NodeletConfig & {
  status?: "ok" | "failed" | "checking";
  statusError?: string;
};

// ---- NodeletManagementView ----
export function NodeletManagementView() {
  const [nodelets, setNodelets] = React.useState<NodeletRow[]>([]);
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState("");

  const [showForm, setShowForm] = React.useState(false);
  const [editItem, setEditItem] = React.useState<NodeletConfig | null>(null);

  async function fetchNodelets() {
    setLoading(true);
    setError("");
    try {
      const configs = await apiRequest<NodeletConfig[]>("/api/nodelets");
      setNodelets(configs.map((c) => ({ ...c })));
      // 异步检测每个 nodelet 连通性
      for (const c of configs) {
        checkStatus(c);
      }
    } catch (err) {
      setError(getErrorMessage(err, "读取服务器列表失败"));
    } finally {
      setLoading(false);
    }
  }

  async function checkStatus(cfg: NodeletConfig) {
    setNodelets((prev) =>
      prev.map((n) => (n.id === cfg.id ? { ...n, status: "checking" } : n)),
    );
    try {
      const resp = await apiRequest<{ status: string; error?: string }>(
        "/api/nodelets/test",
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ id: cfg.id, name: cfg.name, address: cfg.address }),
        },
      );
      setNodelets((prev) =>
        prev.map((n) =>
          n.id === cfg.id
            ? { ...n, status: resp.status === "ok" ? "ok" : "failed", statusError: resp.error }
            : n,
        ),
      );
    } catch {
      setNodelets((prev) =>
        prev.map((n) => (n.id === cfg.id ? { ...n, status: "failed", statusError: "unreachable" } : n)),
      );
    }
  }

  React.useEffect(() => {
    fetchNodelets();
  }, []);

  function openAdd() {
    setEditItem(null);
    setShowForm(true);
  }

  function openEdit(item: NodeletRow) {
    setEditItem(item);
    setShowForm(true);
  }

  async function handleDelete(id: string) {
    if (!window.confirm(`确定要删除服务器 "${id}" 吗？`)) return;
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

  return (
    <section className="panel">
      <div className="panel-summary">
        <div className="section-header-spread">
          <h2 className="section-header">
            <Server size={18} />
            <span>Servers</span>
          </h2>
          <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
            <span style={{ color: "var(--clr-text-muted)", fontSize: 13 }}>
              {nodelets.length} server{nodelets.length !== 1 ? "s" : ""}
            </span>
            <Button size="sm" onClick={openAdd}>
              <Plus size={14} /> Add Server
            </Button>
            <Button size="sm" variant="ghost" onClick={fetchNodelets} disabled={loading}>
              Refresh
            </Button>
          </div>
        </div>
      </div>

      {error && <div className="error-banner">{error}</div>}

      {loading && nodelets.length === 0 ? (
        <div className="skeleton-block" style={{ height: 120 }} />
      ) : nodelets.length === 0 ? (
        <div className="empty-state">
          <p>No servers configured</p>
          <p style={{ color: "var(--clr-text-muted)", marginTop: 4 }}>
            Add a nodelet server to start monitoring containers.
          </p>
        </div>
      ) : (
        <div className="mcp-table-wrap">
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Address</th>
                <th style={{ width: 80 }}>Status</th>
                <th style={{ width: 120 }}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {nodelets.map((n) => (
                <tr key={n.id}>
                  <td>
                    <span style={{ fontWeight: 500 }}>{n.name || n.id}</span>
                  </td>
                  <td style={{ fontFamily: "JetBrains Mono, monospace", fontSize: 12 }}>
                    {n.address}
                  </td>
                  <td>
                    {n.status === "checking" ? (
                      <Loader2 size={14} className="spinner-sm" style={{ color: "var(--clr-text-muted)" }} />
                    ) : n.status === "ok" ? (
                      <span className="status-dot" style={{ backgroundColor: "var(--clr-success)" }} title="reachable" />
                    ) : n.status === "failed" ? (
                      <span className="status-dot" style={{ backgroundColor: "var(--clr-error)", cursor: "help" }} title={n.statusError || "unreachable"} />
                    ) : (
                      <span className="status-dot" style={{ backgroundColor: "var(--clr-text-muted)" }} title="unknown" />
                    )}
                  </td>
                  <td>
                    <div style={{ display: "flex", gap: 4 }}>
                      <Button size="xs" variant="ghost" onClick={() => openEdit(n)}>
                        <Edit3 size={12} />
                      </Button>
                      <Button size="xs" variant="ghost" onClick={() => handleDelete(n.id)}>
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
