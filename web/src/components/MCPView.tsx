import React from "react";
import { Plus, Trash2, Edit3, Wrench, ChevronRight, ChevronDown } from "lucide-react";
import type { MCPConnectionConfig, MCPConnectionStatus } from "../types";
import { mcpStatusLabel } from "../types";
import { apiRequest, getErrorMessage } from "../lib/api";
import { MCPFormModal } from "./MCPFormModal";
import { StatusPill, TypePill } from "./StatusPill";
import { ToggleSwitch } from "./ToggleSwitch";
import { MCPToolList } from "./MCPToolList";
import { Button } from "./ui/Button";

// ---- MCPView ----
export function MCPView() {
  const [connections, setConnections] = React.useState<MCPConnectionStatus[]>([]);
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState("");

  // 表单模态框控制
  const [showForm, setShowForm] = React.useState(false);
  const [editItem, setEditItem] = React.useState<MCPConnectionStatus | null>(null);

  const [toggling, setToggling] = React.useState<Set<string>>(new Set());
  const [checking, setChecking] = React.useState<Set<string>>(new Set());
  const [expandedRows, setExpandedRows] = React.useState<Set<string>>(new Set());

  function setCheckingIds(update: (prev: Set<string>) => Set<string>) {
    setChecking((prev) => {
      const next = update(prev);
      return next;
    });
  }

  function replaceConnection(nextItem: MCPConnectionStatus) {
    setConnections((prev) => prev.map((item) => (item.id === nextItem.id ? nextItem : item)));
  }

  async function fetchConnections() {
    setLoading(true);
    setError("");
    try {
      const next = await apiRequest<MCPConnectionStatus[]>("/api/mcp/connections");
      setConnections(next);
    } catch (err) {
      setError(getErrorMessage(err, "读取 MCP 连接失败"));
    } finally {
      setLoading(false);
    }
  }

  async function refreshConnectionsQuietly() {
    try {
      const next = await apiRequest<MCPConnectionStatus[]>("/api/mcp/connections");
      setConnections(next);
      setCheckingIds((prev) => {
        const nextChecking = new Set(prev);
        for (const item of next) {
          if (item.status === "running" || (item.status === "error" && item.error) || !item.enabled) {
            nextChecking.delete(item.id);
          }
        }
        return nextChecking;
      });
      return next;
    } catch (_err) {
      // 轮询静默失败，不覆盖已有数据和错误展示
      return null;
    }
  }

  React.useEffect(() => {
    fetchConnections();
    const interval = setInterval(refreshConnectionsQuietly, 30_000);
    return () => { clearInterval(interval); };
  }, []);

  React.useEffect(() => {
    if (checking.size === 0) return;
    const startedAt = Date.now();
    const interval = setInterval(async () => {
      await refreshConnectionsQuietly();
      if (Date.now() - startedAt >= 60_000) {
        setCheckingIds(() => new Set());
      }
    }, 1_000);
    return () => clearInterval(interval);
  }, [checking.size]);

  function openAdd() {
    setEditItem(null);
    setShowForm(true);
  }

  function openEdit(item: MCPConnectionStatus) {
    setEditItem(item);
    setShowForm(true);
  }

  async function handleDelete(id: string) {
    if (!window.confirm(`确定要删除 MCP 连接 "${id}" 吗？`)) return;
    try {
      await apiRequest(`/api/mcp/connections/${encodeURIComponent(id)}`, { method: "DELETE" });
      fetchConnections();
    } catch (err) {
      setError(getErrorMessage(err, "删除失败"));
    }
  }

  async function handleToggleEnabled(item: MCPConnectionStatus, enabled: boolean) {
    setToggling((prev) => new Set(prev).add(item.id));
    const body: MCPConnectionConfig = {
      id: item.id,
      name: item.name,
      type: item.type,
      command: item.command,
      args: item.args,
      env: item.env,
      enabled,
      containerId: item.containerId,
      nodeletId: item.nodeletId,
    };
    try {
      await apiRequest(`/api/mcp/connections/${encodeURIComponent(item.id)}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      if (enabled) {
        replaceConnection({ ...item, enabled, status: "stopped", error: undefined });
        setCheckingIds((prev) => new Set(prev).add(item.id));
        await refreshConnectionsQuietly();
      } else {
        replaceConnection({
          ...item,
          enabled,
          status: "stopped",
          error: undefined,
          toolCount: 0,
          tools: undefined,
        });
        setCheckingIds((prev) => {
          const next = new Set(prev);
          next.delete(item.id);
          return next;
        });
        refreshConnectionsQuietly();
      }
    } catch (err) {
      setError(getErrorMessage(err, "更新失败"));
    } finally {
      setToggling((prev) => {
        const next = new Set(prev);
        next.delete(item.id);
        return next;
      });
    }
  }

  function toggleExpand(id: string) {
    setExpandedRows((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  return (
    <section className="panel">
      <div className="panel-summary">
        <Wrench size={16} />
        <span>{loading ? "读取中" : `${connections.length} 个 MCP 连接`}</span>
        <Button size="sm" onClick={openAdd}>
          <Plus size={15} />
          <span>新增</span>
        </Button>
        <Button variant="ghost" size="sm" onClick={fetchConnections} disabled={loading}>
          <span>刷新</span>
        </Button>
      </div>

      {error && <div className="error-banner">{error}</div>}

      <div className="mcp-table-wrap">
        <table>
          <colgroup>
            <col className="mcp-col-name" />
            <col className="mcp-col-type" />
            <col className="mcp-col-status" />
            <col className="mcp-col-count" />
            <col className="mcp-col-command" />
            <col className="mcp-col-toggle" />
            <col className="mcp-col-actions" />
          </colgroup>
          <thead>
            <tr>
              <th>名称</th>
              <th>类型</th>
              <th>状态</th>
              <th>工具数</th>
              <th>命令</th>
              <th>启用</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            {connections.length === 0 && loading ? (
              <tr>
                <td colSpan={7} className="mcp-table-loading">
                  <span className="status-dot loading" />
                  <span>读取状态中</span>
                </td>
              </tr>
            ) : connections.length === 0 ? (
              <tr>
                <td colSpan={7} className="mcp-table-empty">
                  暂无 MCP 连接，点击"新增"创建
                </td>
              </tr>
            ) : (
              connections.map((item) => {
                const isExpanded = expandedRows.has(item.id);
                const hasTools = item.status === "running" && item.tools && item.tools.length > 0;
                const isChecking = checking.has(item.id);
                return (
                  <React.Fragment key={item.id}>
                    <tr className={isExpanded ? "mcp-row-expanded" : ""}>
                      <td className="mcp-name-cell">{item.name}</td>
                      <td>
                        <TypePill label={item.type} />
                      </td>
                      <td>
                        <span className="mcp-status-inline">
                          <StatusPill status={item.status} labelMap={mcpStatusLabel} />
                          <span className="mcp-check-slot">
                            {isChecking && <span className="spinner spinner-sm" title="检查中" />}
                          </span>
                        </span>
                        {item.error && <span className="error-hint">{item.error}</span>}
                      </td>
                      <td className="mcp-count-cell">
                        <button
                          className={`mcp-expand-btn ${hasTools ? "" : "disabled"}`}
                          disabled={!hasTools}
                          onClick={() => hasTools && toggleExpand(item.id)}
                          title={hasTools ? "展开/折叠工具列表" : "无运行中工具"}
                        >
                          {isExpanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                          <span>{item.toolCount}</span>
                        </button>
                      </td>
                      <td className="mono">
                        <span className="mcp-command-text" title={item.command}>{item.command}</span>
                      </td>
                      <td className="mcp-toggle-cell">
                        <ToggleSwitch
                          checked={item.enabled}
                          disabled={toggling.has(item.id)}
                          onChange={(enabled) => handleToggleEnabled(item, enabled)}
                        />
                      </td>
                      <td className="mcp-actions-cell">
                        <div className="mcp-actions">
                          <Button variant="ghost" size="sm" aria-label={`编辑 ${item.name}`} onClick={() => openEdit(item)}>
                            <Edit3 size={14} />
                          </Button>
                          <Button variant="ghost" size="sm" danger aria-label={`删除 ${item.name}`} onClick={() => handleDelete(item.id)}>
                            <Trash2 size={14} />
                          </Button>
                        </div>
                      </td>
                    </tr>
                    {isExpanded && hasTools && (
                      <tr className="mcp-tools-row">
                        <td colSpan={7}>
                          <div className="mcp-tools-list">
                            <MCPToolList connectionId={item.id} tools={item.tools!} onRefreshTools={fetchConnections} />
                          </div>
                        </td>
                      </tr>
                    )}
                  </React.Fragment>
                );
              })
            )}
          </tbody>
        </table>
      </div>

      {/* MCP 表单模态框 */}
      {showForm && (
        <MCPFormModal
          editItem={editItem}
          onClose={() => {
            setShowForm(false);
            setEditItem(null);
          }}
          onSaved={() => {
            fetchConnections();
          }}
        />
      )}
    </section>
  );
}
