import React from "react";
import { ArrowLeft } from "lucide-react";
import type {
  Project,
  ServerWithNodelet,
  ContainerWithType,
  ContainerDetail as ContainerDetailType,
  HealthResult,
  LogEntry,
} from "../types";
import { ServerTree } from "./ServerTree";
import { ContainerDetailView } from "./ContainerDetail";
import { Button } from "./ui/Button";

export function ProjectDetailView({
  project,
  servers,
  serversLoading,
  serverError,
  containers,
  containersLoading,
  selectedNodeletID,
  selectedContainerID,
  containerDetail,
  containerDetailLoading,
  containerDetailError,
  health,
  healthLoading,
  expandedServers,
  logs,
  logsLoading,
  logsError,
  autoScroll,
  onBack,
  onToggleServer,
  onSelectContainer,
  onHealthCheck,
  onAutoScrollChange,
  onClearLogs,
  logsPanelRef,
  onMCPChanged,
}: {
  project: Project;
  servers: ServerWithNodelet[];
  serversLoading: boolean;
  serverError: string;
  containers: Record<string, ContainerWithType[]>;
  containersLoading: Set<string>;
  selectedNodeletID: string;
  selectedContainerID: string;
  containerDetail?: ContainerDetailType;
  containerDetailLoading: boolean;
  containerDetailError: string;
  health?: HealthResult;
  healthLoading: boolean;
  expandedServers: Set<string>;
  logs: LogEntry[];
  logsLoading: boolean;
  logsError: string;
  autoScroll: boolean;
  onBack: () => void;
  onToggleServer: (id: string) => void;
  onSelectContainer: (nodeletID: string, containerID: string) => void;
  onHealthCheck: () => void;
  onAutoScrollChange: (v: boolean) => void;
  onClearLogs: () => void;
  logsPanelRef: React.RefObject<HTMLDivElement | null>;
  onMCPChanged: () => void;
}) {
  const nodeletAddress = servers.find((s) => s.nodelet.id === selectedNodeletID)?.nodelet.address;

  return (
    <div className="project-detail">
      {/* 面包屑 */}
      <div className="project-breadcrumb">
        <Button variant="ghost" onClick={onBack}>
          <ArrowLeft size={16} />
          <span>项目列表</span>
        </Button>
        <span className="breadcrumb-sep">/</span>
        <strong>{project.name}</strong>
      </div>

      {/* 左右分栏 */}
      <div className="project-split">
        <ServerTree
          projectId={project.id}
          servers={servers}
          serversLoading={serversLoading}
          serverError={serverError}
          containers={containers}
          containersLoading={containersLoading}
          selectedContainerID={selectedContainerID}
          expandedServers={expandedServers}
          onToggleServer={onToggleServer}
          onSelectContainer={onSelectContainer}
        />

        <ContainerDetailView
          detail={containerDetail}
          loading={containerDetailLoading}
          error={containerDetailError}
          health={health}
          healthLoading={healthLoading}
          onHealthCheck={onHealthCheck}
          logs={logs}
          logsLoading={logsLoading}
          logsError={logsError}
          autoScroll={autoScroll}
          onAutoScrollChange={onAutoScrollChange}
          onClearLogs={onClearLogs}
          logsPanelRef={logsPanelRef}
          nodeletId={selectedNodeletID}
          containerId={selectedContainerID}
          projectId={project.id}
          nodeletAddress={nodeletAddress}
          onMCPChanged={onMCPChanged}
        />
      </div>
    </div>
  );
}
