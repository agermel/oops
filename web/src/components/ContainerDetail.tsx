import React from "react";
import { Info, Gauge, Wrench, FileText, Settings } from "lucide-react";
import type { ContainerDetail as ContainerDetailType, HealthResult, LogEntry } from "../types";
import { ContainerOverview } from "./ContainerOverview";
import { ContainerHealth } from "./ContainerHealth";
import { ContainerMCP } from "./ContainerMCP";
import { ContainerLogs } from "./ContainerLogs";
import { ContainerDSN } from "./ContainerDSN";

const TABS = [
  { id: "overview", label: "概览", icon: Info },
  { id: "health", label: "健康", icon: Gauge },
  { id: "dsn", label: "DSN 配置", icon: Settings },
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
  nodeletId,
  containerId,
  projectId,
  nodeletAddress,
  onMCPChanged,
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
  nodeletId: string;
  containerId: string;
  projectId: string;
  nodeletAddress?: string;
  onMCPChanged: () => void;
}) {
  const [activeTab, setActiveTab] = React.useState<TabID>("overview");

  function handleEditDSN() {
    setActiveTab("dsn");
  }

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
      </div>

      {error && <div className="error-banner">{error}</div>}

      {/* Tab content —— 懒加载：仅当前活跃 tab 挂载组件，避免隐藏 tab 无谓发 API */}
      <div className="detail-body">
        {loading && !detail ? (
          <div className="loading-overlay"><span className="spinner" /> 加载容器详情...</div>
        ) : !detail ? (
          <div className="empty-state">从左侧选择一个容器查看详情</div>
        ) : (
          <>
            {activeTab === "overview" && (
              <div role="tabpanel"><ContainerOverview detail={detail} onEditDSN={handleEditDSN} /></div>
            )}
            {activeTab === "health" && (
              <div role="tabpanel">
                <ContainerHealth health={health || detail.health} loading={healthLoading} onCheck={onHealthCheck} />
              </div>
            )}
            {activeTab === "dsn" && (
              <div role="tabpanel">
                <ContainerDSN
                  projectId={projectId}
                  nodeletId={nodeletId}
                  containerId={containerId}
                  serviceType={detail.serviceType}
                  onChanged={onMCPChanged}
                />
              </div>
            )}
            {activeTab === "mcp" && (
              <div role="tabpanel">
                <ContainerMCP
                  mcp={detail.mcp}
                  projectId={projectId}
                  nodeletId={nodeletId}
                  containerId={containerId}
                  containerName={detail.container.name}
                  serviceType={detail.serviceType}
                  dsn={detail.dsn}
                  nodeletAddress={nodeletAddress}
                  containerPorts={detail.container.ports}
                  onMCPChanged={onMCPChanged}
                />
              </div>
            )}
            {activeTab === "logs" && (
              <div role="tabpanel">
                <ContainerLogs
                  key={containerId}
                  logs={logs}
                  loading={logsLoading}
                  error={logsError}
                  autoScroll={autoScroll}
                  onAutoScrollChange={onAutoScrollChange}
                  onClear={onClearLogs}
                  panelRef={logsPanelRef}
                />
              </div>
            )}
          </>
        )}
      </div>
    </div>
  );
}
