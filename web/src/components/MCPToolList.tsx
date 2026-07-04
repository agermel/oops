import React from "react";
import { Play, RotateCw, AlertTriangle, WifiOff, XCircle } from "lucide-react";
import type { ToolInfo, ToolTestResult } from "../types";
import { getErrorMessage } from "../lib/api";
import { mcpConnectionPaths } from "../lib/paths";
import { Button } from "./ui/Button";

// toolTestStatus 返回测试结果的显示信息。
function toolTestStatus(tr: ToolTestResult | undefined, testing: boolean) {
  if (testing) return { icon: <span className="tool-status tool-testing" title="测试中…">⟳</span>, cls: "" };
  if (!tr || tr.status === "untested") return { icon: <span className="tool-status tool-untested" title="未测试">○</span>, cls: "" };
  switch (tr.status) {
    case "ok":
      return { icon: <span className="tool-status tool-ok" title="测试通过">✓</span>, cls: "tool-detail-ok" };
    case "unavailable":
      return { icon: <span className="tool-status tool-warn" title="连接未运行"><AlertTriangle size={14} /></span>, cls: "tool-detail-warn" };
    case "transport_error":
      return { icon: <span className="tool-status tool-err" title="传输层错误"><WifiOff size={14} /></span>, cls: "tool-detail-error" };
    case "error":
      return { icon: <span className="tool-status tool-err" title="测试失败"><XCircle size={14} /></span>, cls: "tool-detail-error" };
    default:
      return { icon: <span className="tool-status tool-untested" title="未测试">○</span>, cls: "" };
  }
}

export function MCPToolList({
  connectionId,
  tools,
  onRefreshTools,
}: {
  connectionId: string;
  tools: ToolInfo[];
  onRefreshTools?: () => void;
}) {
  const [toolTests, setToolTests] = React.useState<Record<string, ToolTestResult>>({});
  const [testingTools, setTestingTools] = React.useState<Set<string>>(new Set());
  // 用 ref 保留测试结果，父组件刷新时不清除。
  const toolTestsRef = React.useRef<Record<string, ToolTestResult>>({});

  // Keep ref in sync with state — no side effects inside setState updaters.
  React.useEffect(() => {
    toolTestsRef.current = toolTests;
  }, [toolTests]);

  async function testTool(toolName: string) {
    const key = `${connectionId}:${toolName}`;
    setTestingTools((prev) => new Set(prev).add(key));
    setToolTests((prev) => ({ ...prev, [key]: { status: "testing" } }));
    try {
      const resp = await fetch(
        mcpConnectionPaths.toolTest(connectionId, toolName),
        { method: "POST" },
      );
      const data = await resp.json().catch(() => null);
      if (!resp.ok) {
        // HTTP 错误：区分后端不可达 (502/503) 和业务错误 (4xx)
        const isTransport = resp.status >= 500 || resp.status === 0;
        const result: ToolTestResult = {
          status: isTransport ? "transport_error" : (data?.status || "error"),
          error: data?.error || `HTTP ${resp.status}`,
        };
        setToolTests((prev) => ({ ...prev, [key]: result }));
        return;
      }
      const result: ToolTestResult = data?.status === "ok"
        ? { status: "ok", output: data.output }
        : { status: data?.status || "error", error: data?.error || "未知错误" };
      setToolTests((prev) => ({ ...prev, [key]: result }));
    } catch (err) {
      // fetch 本身抛出的异常（网络断开等）→ 传输层错误
      const result: ToolTestResult = { status: "transport_error", error: getErrorMessage(err, "网络请求失败") };
      setToolTests((prev) => ({ ...prev, [key]: result }));
    } finally {
      setTestingTools((prev) => {
        const next = new Set(prev);
        next.delete(key);
        return next;
      });
    }
  }

  // 刷新时保留已有测试结果。
  React.useEffect(() => {
    setToolTests(toolTestsRef.current);
  }, [tools]);

  return (
    <div className="mcp-tools-subwrap">
      <div className="mcp-tools-subhead">
        <span className="mcp-tools-subtitle">工具列表</span>
        {onRefreshTools && (
          <Button variant="ghost" size="sm" onClick={onRefreshTools} title="刷新工具列表">
            <RotateCw size={12} />
            <span>刷新</span>
          </Button>
        )}
      </div>
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
            const { icon, cls } = toolTestStatus(tr, testing);
            const showDetail = tr && (tr.status === "ok" || tr.status === "error" || tr.status === "unavailable" || tr.status === "transport_error");
            return (
              <React.Fragment key={t.name}>
                <tr className="mcp-tool-row">
                  <td>{icon}</td>
                  <td><code>{t.name}</code></td>
                  <td className="mcp-tool-desc">{t.description}</td>
                  <td>
                    <Button
                      variant="ghost"
                      size="sm"
                      disabled={testing}
                      onClick={() => testTool(t.name)}
                    >
                      <Play size={12} />
                      <span>{testing ? "测试中" : "测试"}</span>
                    </Button>
                  </td>
                </tr>
                {showDetail && (
                  <tr className="tool-test-detail-row">
                    <td colSpan={4}>
                      <div className={`tool-test-detail-body ${cls}`}>
                        {tr!.status === "error" || tr!.status === "unavailable" || tr!.status === "transport_error"
                          ? tr!.error
                          : tr!.output}
                      </div>
                    </td>
                  </tr>
                )}
              </React.Fragment>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
