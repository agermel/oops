import React from "react";
import { useQuery } from "@tanstack/react-query";
import { Eye, X } from "lucide-react";
import type { MCPResource } from "../types";
import { apiRequest, getErrorMessage } from "../lib/api";
import { mcpConnectionPaths } from "../lib/paths";
import { queryKeys } from "../hooks/queries";
import { Button } from "./ui/Button";

// ResourceContent 对应后端 ReadResourceResult 里的单个 content：文本或二进制。
type ResourceContent = {
  uri?: string;
  mimeType?: string;
  text?: string;
  blob?: string;
};

type ReadResourceResponse = { contents: ResourceContent[] };

function formatSize(size?: number) {
  if (size == null) return "-";
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`;
  return `${(size / 1024 / 1024).toFixed(1)} MB`;
}

// MCPResourceList 展示一个 server 暴露的只读资源列表，点击「查看」按需读取正文。
export function MCPResourceList({
  connectionId,
  resources,
}: {
  connectionId: string;
  resources: MCPResource[];
}) {
  const [openUri, setOpenUri] = React.useState<string | null>(null);

  // 正文按需读取：仅当点开了某个 URI 才发起请求（TanStack Query 缓存结果）。
  const { data, isLoading, error } = useQuery({
    queryKey: queryKeys.mcp.resourceRead(connectionId, openUri ?? ""),
    queryFn: () =>
      apiRequest<ReadResourceResponse>(mcpConnectionPaths.readResource(connectionId, openUri!)),
    enabled: !!openUri,
  });

  if (resources.length === 0) return <div className="empty-state">无资源</div>;

  return (
    <div className="mcp-tools-subwrap">
      <table className="mcp-tools-subtable">
        <thead>
          <tr>
            <th className="mcp-tool-col-name">名称</th>
            <th>URI</th>
            <th>类型</th>
            <th className="mcp-tool-col-desc">描述</th>
            <th>大小</th>
            <th>查看</th>
          </tr>
        </thead>
        <tbody>
          {resources.map((r) => {
            const open = openUri === r.uri;
            return (
              <React.Fragment key={r.uri}>
                <tr>
                  <td>
                    <code>{r.name || r.uri}</code>
                    {r.title && <span className="mcp-tool-original"> {r.title}</span>}
                  </td>
                  <td className="mcp-resource-uri">{r.uri}</td>
                  <td>{r.mimeType || "-"}</td>
                  <td className="mcp-tool-desc">{r.description || "-"}</td>
                  <td>{formatSize(r.size)}</td>
                  <td>
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => setOpenUri(open ? null : r.uri)}
                    >
                      {open ? <X size={12} /> : <Eye size={12} />}
                      <span>{open ? "收起" : "查看"}</span>
                    </Button>
                  </td>
                </tr>
                {open && (
                  <tr className="tool-test-detail-row">
                    <td colSpan={6}>
                      <div className="mcp-resource-body">
                        {isLoading && <div className="muted">读取中…</div>}
                        {error && (
                          <div className="error-text">{getErrorMessage(error, "读取资源失败")}</div>
                        )}
                        {data && <ResourceContentsView contents={data.contents} />}
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

function ResourceContentsView({ contents }: { contents: ResourceContent[] }) {
  if (!contents || contents.length === 0) return <div className="muted">（空内容）</div>;
  return (
    <>
      {contents.map((c, i) => {
        if (c.text != null) {
          return (
            <pre key={i} className="mcp-resource-text">
              {c.text}
            </pre>
          );
        }
        return (
          <div key={i} className="muted">
            二进制内容（base64，长度 {c.blob?.length ?? 0}），无法预览
          </div>
        );
      })}
    </>
  );
}
