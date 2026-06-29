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
  const [expandedRows, setExpandedRows] = React.useState<Set<string>>(new Set());

  async function fetchConnections() {
    setLoading(true);
    setError("");
    try {
      setConnections(await apiRequest<MCPConnectionStatus[]>("/api/mcp/connections"));
    } catch (err) {
      setError(getErrorMessage(err, "读取 MCP 连接失败"));
    } finally {
      setLoading(false);
    }
  }

  React.useEffect(() => {
    fetchConnections();
  }, []);

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

  async function handleToggleEnabled(item: MCPConnectionStatus) {
    setToggling((prev) => new Set(prev).add(item.id));
    const body: MCPConnectionConfig = {
      id: item.id,
      name: item.name,
      type: item.type,
      command: item.command,
      args: item.args,
      env: item.env,
      enabled: !item.enabled,
    };
    try {
      await apiRequest(`/api/mcp/connections/${encodeURIComponent(item.id)}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      fetchConnections();
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
                return (
                  <React.Fragment key={item.id}>
                    <tr className={isExpanded ? "mcp-row-expanded" : ""}>
                      <td className="mcp-name-cell">{item.name}</td>
                      <td>
                        <TypePill label={item.type} />
                      </td>
                      <td>
                        <StatusPill status={item.status} labelMap={mcpStatusLabel} />
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
                      <td className="mono">{item.command}</td>
                      <td className="mcp-toggle-cell">
                        <ToggleSwitch
                          checked={item.enabled}
                          disabled={toggling.has(item.id)}
                          onChange={() => handleToggleEnabled(item)}
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
                            <MCPToolList connectionId={item.id} tools={item.tools!} />
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
