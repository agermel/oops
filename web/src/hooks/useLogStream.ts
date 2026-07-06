import { serverPaths } from "../lib/paths";
import { useEventLogStream } from "./useEventLogStream";

export function useLogStream(projectId: string, nodeletId: string, containerId: string) {
  const activeKey = containerId ? `${nodeletId}/${containerId}` : "";
  const url = containerId ? serverPaths(projectId, nodeletId).containerLogs(containerId) : "";
  return useEventLogStream({
    activeKey,
    url,
    errorMessage: "日志流连接失败，请检查容器是否在运行",
  });
}
