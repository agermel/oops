import { Wrench } from "lucide-react";
import type { MCPStatus } from "../types";

export function ContainerMCP({ mcp }: { mcp?: MCPStatus }) {
  return (
    <div className="container-mcp">
      <div className="section-title">
        <Wrench size={18} />
        <h2>MCP 连接</h2>
      </div>

      {mcp ? (
        <div className="mcp-status-card">
          <div className="mcp-status-row">
            <span>状态</span>
            <span className={`status-pill ${mcp.connected ? "alive" : "dead"}`}>
              {mcp.connected ? "已连接" : "未连接"}
            </span>
          </div>
          <div className="mcp-status-row">
            <span>工具数</span>
            <strong>{mcp.toolCount}</strong>
          </div>
          {mcp.error && (
            <div className="mcp-status-row">
              <span>错误</span>
              <span className="error-hint">{mcp.error}</span>
            </div>
          )}
        </div>
      ) : (
        <div className="empty-card">MCP 连接未配置</div>
      )}
    </div>
  );
}
