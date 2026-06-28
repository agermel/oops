import React from "react";
import { Cog, RefreshCw } from "lucide-react";
import { apiRequest, getErrorMessage } from "../lib/api";
import { ToggleSwitch } from "./ToggleSwitch";

// ---- 类型 ----

type ToolItem = {
  name: string;
  description: string;
  enabled: boolean;
};

type ToolsData = {
  native: ToolItem[];
  mcp: Record<string, ToolItem[]>;
};

// ---- 组件 ----

export function ToolsView() {
  const [data, setData] = React.useState<ToolsData | null>(null);
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState("");

  // 每个 toggle 的独立 loading 状态：key=toolName → true 表示正在请求
  const [toggling, setToggling] = React.useState<Record<string, boolean>>({});

  async function fetchTools() {
    setLoading(true);
    setError("");
    try {
      setData(await apiRequest<ToolsData>("/api/tools"));
    } catch (err) {
      setError(getErrorMessage(err, "读取工具列表失败"));
    } finally {
      setLoading(false);
    }
  }

  React.useEffect(() => {
    fetchTools();
  }, []);

  async function toggleTool(name: string, enabled: boolean) {
    setToggling((prev) => ({ ...prev, [name]: true }));
    // 乐观更新
    setData((prev) => {
      if (!prev) return prev;
      const update = (items: ToolItem[]) =>
        items.map((t) => (t.name === name ? { ...t, enabled } : t));
      return {
        native: update(prev.native),
        mcp: Object.fromEntries(
          Object.entries(prev.mcp).map(([conn, tools]) => [conn, update(tools)])
        ),
      };
    });

    try {
      await apiRequest(`/api/tools/${encodeURIComponent(name)}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ enabled }),
      });
    } catch {
      // 失败时回滚
      fetchTools();
    } finally {
      setToggling((prev) => {
        const next = { ...prev };
        delete next[name];
        return next;
      });
    }
  }

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
        <button className="ghost-button small" onClick={fetchTools} disabled={loading}>
          <RefreshCw size={14} className={loading ? "spin" : ""} />
          <span>刷新</span>
        </button>
      </div>

      {error && <div className="error-line">{error}</div>}

      {!loading && !hasContent && (
        <div className="empty-card">暂无可用的 LLM 工具</div>
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
                    toggling={!!toggling[tool.name]}
                    onToggle={(enabled) => toggleTool(tool.name, enabled)}
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
                      toggling={!!toggling[tool.name]}
                      onToggle={(enabled) => toggleTool(tool.name, enabled)}
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
