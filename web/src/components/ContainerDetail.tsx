import React from "react";
import { Info, Gauge, Wrench, FileText, RefreshCw } from "lucide-react";
import type { ContainerDetail as ContainerDetailType, HealthResult, LogEntry, MCPPrefill } from "../types";
import { ContainerOverview } from "./ContainerOverview";
import { ContainerHealth } from "./ContainerHealth";
import { ContainerMCP } from "./ContainerMCP";
import { ContainerLogs } from "./ContainerLogs";

const TABS = [
  { id: "overview", label: "概览", icon: Info },
  { id: "health", label: "健康", icon: Gauge },
  { id: "mcp", label: "MCP", icon: Wrench },
  { id: "logs", label: "日志", icon: FileText },
] as const;

type TabID = (typeof TABS)[number]["id"];

export function ContainerDetailView({
  detail,
  loading,
  error,
  health,
  healthLoading,
  onHealthCheck,
  logs,
  logsLoading,
  logsError,
  autoScroll,
  onAutoScrollChange,
  onClearLogs,
  logsPanelRef,
  onConfigureMCP,
}: {
  detail?: ContainerDetailType;
  loading: boolean;
  error: string;
  health?: HealthResult;
  healthLoading: boolean;
  onHealthCheck: () => void;
  logs: LogEntry[];
  logsLoading: boolean;
  logsError: string;
  autoScroll: boolean;
  onAutoScrollChange: (v: boolean) => void;
  onClearLogs: () => void;
  logsPanelRef: React.RefObject<HTMLDivElement | null>;
  onConfigureMCP: (prefill: MCPPrefill) => void;
}) {
  const [activeTab, setActiveTab] = React.useState<TabID>("overview");

  return (
    <div className="container-detail">
      {/* Tab bar */}
      <div className="detail-tabs" role="tablist" aria-label="容器详情标签页">
        {TABS.map((tab) => (
          <button
            key={tab.id}
            role="tab"
            aria-selected={activeTab === tab.id}
            className={activeTab === tab.id ? "active" : ""}
            onClick={() => setActiveTab(tab.id)}
          >
            <tab.icon size={15} />
            <span>{tab.label}</span>
          </button>
        ))}
        {loading && <RefreshCw size={15} className="spin" />}
      </div>

      {error && <div className="error-line">{error}</div>}

      {/* Tab content */}
      <div className="detail-body">
        {loading && !detail ? (
          <div className="empty-card">正在加载容器详情</div>
        ) : !detail ? (
          <div className="empty-card">从左侧选择一个容器查看详情</div>
        ) : (
          <>
            <div role="tabpanel" hidden={activeTab !== "overview"}>
              {activeTab === "overview" && <ContainerOverview detail={detail} onConfigureMCP={onConfigureMCP} />}
            </div>
            <div role="tabpanel" hidden={activeTab !== "health"}>
              {activeTab === "health" && (
                <ContainerHealth health={health || detail.health} loading={healthLoading} onCheck={onHealthCheck} />
              )}
            </div>
            <div role="tabpanel" hidden={activeTab !== "mcp"}>
              {activeTab === "mcp" && <ContainerMCP mcp={detail.mcp} />}
            </div>
            <div role="tabpanel" hidden={activeTab !== "logs"}>
              {activeTab === "logs" && (
                <ContainerLogs
                  logs={logs}
                  loading={logsLoading}
                  error={logsError}
                  autoScroll={autoScroll}
                  onAutoScrollChange={onAutoScrollChange}
                  onClear={onClearLogs}
                  panelRef={logsPanelRef}
                />
              )}
            </div>
          </>
        )}
      </div>
    </div>
  );
}
