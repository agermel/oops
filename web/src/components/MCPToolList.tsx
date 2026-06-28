import React from "react";
import { Play } from "lucide-react";
import type { ToolInfo, ToolTestResult } from "../types";
import { apiRequest, getErrorMessage } from "../lib/api";

export function MCPToolList({
  connectionId,
  tools,
}: {
  connectionId: string;
  tools: ToolInfo[];
}) {
  const [toolTests, setToolTests] = React.useState<Record<string, ToolTestResult>>({});
  const [testingTools, setTestingTools] = React.useState<Set<string>>(new Set());

  async function testTool(toolName: string) {
    const key = `${connectionId}:${toolName}`;
    setTestingTools((prev) => new Set(prev).add(key));
    setToolTests((prev) => ({ ...prev, [key]: { status: "testing" } }));
    try {
      const data = await apiRequest<{ status: string; output?: string; error?: string }>(
        `/api/mcp/connections/${encodeURIComponent(connectionId)}/tools/${encodeURIComponent(toolName)}/test`,
        { method: "POST" },
      );
      if (data.status === "ok") {
        setToolTests((prev) => ({ ...prev, [key]: { status: "ok", output: data.output } }));
      } else {
        setToolTests((prev) => ({ ...prev, [key]: { status: "error", error: data.error } }));
      }
    } catch (err) {
      setToolTests((prev) => ({
        ...prev,
        [key]: { status: "error", error: getErrorMessage(err, "测试失败") },
      }));
    } finally {
      setTestingTools((prev) => {
        const next = new Set(prev);
        next.delete(key);
        return next;
      });
    }
  }

  return (
    <table className="mcp-tools-subtable">
      <thead>
        <tr>
          <th className="mcp-tool-col-status">状态</th>
          <th className="mcp-tool-col-name">工具名称</th>
          <th className="mcp-tool-col-desc">描述</th>
          <th className="mcp-tool-col-test">测试</th>
        </tr>
      </thead>
      <tbody>
        {tools.map((t) => {
          const tkey = `${connectionId}:${t.name}`;
          const tr = toolTests[tkey];
          const testing = testingTools.has(tkey);
          const showDetail = tr && (tr.status === "ok" || tr.status === "error");
          return (
            <React.Fragment key={t.name}>
              <tr className="mcp-tool-row">
                <td>
                  {testing ? (
                    <span className="tool-status tool-testing" title="测试中…">⟳</span>
                  ) : tr?.status === "ok" ? (
                    <span className="tool-status tool-ok" title="测试通过">✓</span>
                  ) : tr?.status === "error" ? (
                    <span className="tool-status tool-err" title="测试失败">✗</span>
                  ) : (
                    <span className="tool-status tool-untested" title="未测试">○</span>
                  )}
                </td>
                <td><code>{t.name}</code></td>
                <td className="mcp-tool-desc">{t.description}</td>
                <td>
                  <button
                    className="ghost-button small"
                    disabled={testing}
                    onClick={() => testTool(t.name)}
                  >
                    <Play size={12} />
                    <span>{testing ? "测试中" : "测试"}</span>
                  </button>
                </td>
              </tr>
              {showDetail && (
                <tr className="tool-test-detail-row">
                  <td colSpan={4}>
                    <div className={`tool-test-detail-body ${tr!.status}`}>
                      {tr!.status === "error" ? tr!.error : tr!.output}
                    </div>
                  </td>
                </tr>
              )}
            </React.Fragment>
          );
        })}
      </tbody>
    </table>
  );
}
