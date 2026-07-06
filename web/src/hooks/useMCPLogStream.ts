import { mcpConnectionPaths } from "../lib/paths";
import { useEventLogStream } from "./useEventLogStream";

export function useMCPLogStream(connectionId: string) {
  return useEventLogStream({
    activeKey: connectionId,
    url: connectionId ? mcpConnectionPaths.logsStream(connectionId) : "",
    errorMessage: "MCP 日志流连接失败，请检查连接状态",
  });
}
