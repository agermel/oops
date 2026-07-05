import React from "react";
import { Cog, RefreshCw } from "lucide-react";
import { getErrorMessage } from "../lib/api";
import { useToolToggle, useTools, type ToolItem } from "../hooks/useTools";
import { ToggleSwitch } from "./ToggleSwitch";
import { Button } from "./ui/Button";

// ---- 组件 ----

export function ToolsView() {
  const { data, isLoading: loading, error: queryError, refetch } = useTools();

  const error = queryError ? getErrorMessage(queryError, "读取工具列表失败") : "";

  const toggleMutation = useToolToggle();

  const hasMCP = data && Object.keys(data.mcp).length > 0;
  const hasContent = data && (data.native.length > 0 || hasMCP);

  return (
    <section className="panel">
      <div className="panel-summary">
        <Cog size={16} />
        <span>
          {loading
            ? "读取中"
            : data
              ? `${data.native.length + Object.values(data.mcp).reduce((sum, t) => sum + t.length, 0)} 个工具`
              : "—"}
        </span>
        <Button variant="ghost" size="sm" onClick={() => refetch()} disabled={loading}>
          <RefreshCw size={14} className={loading ? "spin" : ""} />
          <span>刷新</span>
        </Button>
      </div>

      {error && <div className="error-banner">{error}</div>}

      {!loading && !hasContent && (
        <div className="empty-state">暂无可用的 LLM 工具</div>
      )}

      {data && (
        <div className="tools-list">
          {/* 内置工具 */}
          {data.native.length > 0 && (
            <div className="tools-group">
              <h3 className="tools-group-title">内置工具</h3>
              <div className="tools-cards">
                {data.native.map((tool) => (
                  <ToolRow
                    key={tool.name}
                    tool={tool}
                    toggling={toggleMutation.isPending && toggleMutation.variables?.name === tool.name}
                    onToggle={(enabled) => toggleMutation.mutate({ name: tool.name, enabled })}
                  />
                ))}
              </div>
            </div>
          )}

          {/* MCP 工具（按连接分组） */}
          {hasMCP &&
            Object.entries(data.mcp).map(([connName, tools]) => (
              <div className="tools-group" key={connName}>
                <h3 className="tools-group-title">MCP: {connName}</h3>
                <div className="tools-cards">
                  {tools.map((tool) => (
                    <ToolRow
                      key={tool.name}
                      tool={tool}
                      toggling={toggleMutation.isPending && toggleMutation.variables?.name === tool.name}
                      onToggle={(enabled) => toggleMutation.mutate({ name: tool.name, enabled })}
                    />
                  ))}
                </div>
              </div>
            ))}
        </div>
      )}
    </section>
  );
}

function ToolRow({
  tool,
  toggling,
  onToggle,
}: {
  tool: ToolItem;
  toggling: boolean;
  onToggle: (enabled: boolean) => void;
}) {
  return (
    <div className={`tool-row ${!tool.enabled ? "tool-disabled" : ""}`}>
      <div className="tool-info">
        <code className="tool-name">{tool.name}</code>
        <span className="tool-desc">{tool.description || "—"}</span>
      </div>
      <ToggleSwitch
        checked={tool.enabled}
        disabled={toggling}
        onChange={onToggle}
      />
    </div>
  );
}
