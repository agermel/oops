import React from "react";
import { Plus, Trash2, Edit3, Wrench, ChevronRight, ChevronDown, Database, Layers, Search, Globe, Terminal } from "lucide-react";
import type { MCPConnectionConfig, MCPConnectionStatus } from "../types";
import { mcpStatusLabel } from "../types";
import { useQueryClient } from "@tanstack/react-query";
import { apiRequest, getErrorMessage } from "../lib/api";
import { mcpConnectionPaths } from "../lib/paths";
import { useMCPConnections } from "../hooks/useServers";
import { queryKeys } from "../hooks/queries";
import { useSet } from "../hooks/useSet";
import { useModal } from "../hooks/useModal";
import { MCPFormModal } from "./MCPFormModal";
import { StatusPill, TypePill } from "./StatusPill";
import { ToggleSwitch } from "./ToggleSwitch";
import { MCPToolList } from "./MCPToolList";
import { Button } from "./ui/Button";

// ---- MCPView ----
export function MCPView({
  onQuickCreate,
  onEdit,
}: {
  onQuickCreate?: (type?: string) => void;
  onEdit?: (item: MCPConnectionStatus) => void;
}) {
  const { data: connections = [], isLoading: loading, error: queryError, refetch } = useMCPConnections();
  const error = queryError ? getErrorMessage(queryError, "读取 MCP 连接失败") : "";
  const queryClient = useQueryClient();

  // 表单模态框控制（仅在无外部回调时使用本地状态）
  const localForm = useModal<MCPConnectionStatus>();

  const toggling = useSet();
  const checking = useSet();
  const expandedRows = useSet();

  // 快捷创建卡片定义
  const quickStartTypes = [
    { id: "mysql", label: "MySQL", hint: "云端 MySQL 数据库", icon: Database },
    { id: "redis", label: "Redis", hint: "云端 Redis 服务", icon: Layers },
    { id: "postgres", label: "PostgreSQL", hint: "云端 PostgreSQL 数据库", icon: Database },
    { id: "etcd", label: "Etcd", hint: "Etcd 集群", icon: Globe },
    { id: "elasticsearch", label: "Elasticsearch", hint: "Elasticsearch 集群", icon: Search },
    { id: "other", label: "其他", hint: "自定义命令行连接", icon: Terminal },
  ];

  // 1s 子轮询：当有连接处于 "checking"（刚启用，等待启动）状态时，快速轮询直到状态稳定
  React.useEffect(() => {
    if (checking.set.size === 0) return;
    const startedAt = Date.now();
    const interval = setInterval(async () => {
      const next = await queryClient.fetchQuery<MCPConnectionStatus[]>({
        queryKey: queryKeys.mcp.all,
        queryFn: () => apiRequest<MCPConnectionStatus[]>(mcpConnectionPaths.list),
        staleTime: 0,
      });
      if (next) {
        const nextChecking = new Set(checking.set);
        for (const item of next) {
          if (item.status === "running" || (item.status === "error" && item.error) || !item.enabled) {
            nextChecking.delete(item.id);
          }
        }
        checking.setState(nextChecking);
      }
      if (Date.now() - startedAt >= 60_000) {
        checking.clear();
      }
    }, 1_000);
    return () => clearInterval(interval);
  }, [checking.set.size, queryClient]);

  function openAdd() {
    if (onQuickCreate) { onQuickCreate(); }
    else { localForm.onOpen(); }
  }

  function openEdit(item: MCPConnectionStatus) {
    if (onEdit) { onEdit(item); }
    else { localForm.onOpen(item); }
  }

  async function handleDelete(id: string) {
    if (!window.confirm(`确定要删除 MCP 连接 "${id}" 吗？`)) return;
    try {
      await apiRequest(mcpConnectionPaths.detail(id), { method: "DELETE" });
      queryClient.invalidateQueries({ queryKey: queryKeys.mcp.all });
    } catch (err) {
      alert(getErrorMessage(err, "删除失败"));
    }
  }

  async function handleToggleEnabled(item: MCPConnectionStatus, enabled: boolean) {
    toggling.add(item.id);
    const body: MCPConnectionConfig = {
      id: item.id,
      name: item.name,
      type: item.type,
      transport: item.transport,
      command: item.command,
      args: item.args,
      env: item.env,
      url: item.url,
      enabled,
      containerId: item.containerId,
      nodeletId: item.nodeletId,
    };
    try {
      await apiRequest(mcpConnectionPaths.detail(item.id), {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      if (enabled) {
        checking.add(item.id);
      } else {
        checking.remove(item.id);
      }
      queryClient.invalidateQueries({ queryKey: queryKeys.mcp.all });
      // 立即触发一次刷新以更新 checking 列表中的状态
      if (enabled) {
        setTimeout(() => {
          queryClient.invalidateQueries({ queryKey: queryKeys.mcp.all });
        }, 500);
      }
    } catch (err) {
      alert(getErrorMessage(err, "更新失败"));
    } finally {
      toggling.remove(item.id);
    }
  }

  function toggleExpand(id: string) {
    expandedRows.toggle(id);
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
        <Button variant="ghost" size="sm" onClick={() => refetch()} disabled={loading}>
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
              <th>传输 / 命令</th>
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
                <td colSpan={7} style={{ padding: 0 }}>
                  <div className="mcp-quickstart-section">
                    <h3>快速创建连接</h3>
                    <p className="mcp-quickstart-hint">选择数据库类型，填写云端地址即可连接</p>
                    <div className="mcp-quickstart-grid">
                      {quickStartTypes.map((t) => (
                        <button
                          key={t.id}
                          className="mcp-quickstart-card"
                          onClick={() => onQuickCreate?.(t.id)}
                        >
                          <t.icon size={28} className="mcp-quickstart-icon" />
                          <span className="mcp-quickstart-label">{t.label}</span>
                          <small>{t.hint}</small>
                        </button>
                      ))}
                    </div>
                  </div>
                </td>
              </tr>
            ) : (
              connections.map((item) => {
                const isExpanded = expandedRows.set.has(item.id);
                const hasTools = item.status === "running" && item.tools && item.tools.length > 0;
                const isChecking = checking.set.has(item.id);
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
                        <span className="mcp-command-text" title={item.transport === "sse" ? item.url : item.command}>
                          {item.transport === "sse" ? item.url : item.command}
                        </span>
                      </td>
                      <td className="mcp-toggle-cell">
                        <ToggleSwitch
                          checked={item.enabled}
                          disabled={toggling.set.has(item.id)}
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
                            <MCPToolList connectionId={item.id} tools={item.tools!} onRefreshTools={() => queryClient.invalidateQueries({ queryKey: queryKeys.mcp.all })} />
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

      {/* MCP 表单模态框 —— 仅在无外部 onQuickCreate 时使用本地模态框（编辑功能需要） */}
      {localForm.open && !onQuickCreate && (
        <MCPFormModal
          editItem={localForm.data}
          onClose={localForm.onClose}
          onSaved={() => {
            localForm.onClose();
            queryClient.invalidateQueries({ queryKey: queryKeys.mcp.all });
          }}
        />
      )}
    </section>
  );
}
