import type { MCPPrompt } from "../types";

// MCPPromptList 展示一个 server 暴露的预置提示词模板列表（纯展示，无操作）。
export function MCPPromptList({ prompts }: { prompts: MCPPrompt[] }) {
  if (prompts.length === 0) return <div className="empty-state">无提示词</div>;
  return (
    <div className="mcp-tools-subwrap">
      <table className="mcp-tools-subtable">
        <thead>
          <tr>
            <th className="mcp-tool-col-name">名称</th>
            <th className="mcp-tool-col-desc">描述</th>
            <th>参数</th>
          </tr>
        </thead>
        <tbody>
          {prompts.map((p) => (
            <tr key={p.name}>
              <td>
                <code>{p.name}</code>
                {p.title && <span className="mcp-tool-original"> {p.title}</span>}
              </td>
              <td className="mcp-tool-desc">{p.description || "-"}</td>
              <td>
                {p.arguments && p.arguments.length > 0
                  ? p.arguments
                      .map((a) => `${a.name}${a.required ? "*" : ""}`)
                      .join(", ")
                  : "-"}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
