import React from "react";
import { ArrowLeft, Github, ExternalLink, Check, X, Pencil } from "lucide-react";
import type {
  Project,
  ServerWithNodelet,
  LogEntry,
  ProjectMCPConnection,
} from "../types";
import { ServerTree } from "./ServerTree";
import { ContainerDetailView } from "./ContainerDetail";
import { Button } from "./ui/Button";
import { FormInput } from "./ui/FormInput";
import { getErrorMessage } from "../lib/api";
import { useUpdateProject } from "../hooks/useProjects";
import { useProjectMCPConnections } from "../hooks/useServers";

export function ProjectDetailView({
  project,
  servers,
  serversLoading,
  serverError,
  selectedNodeletID,
  selectedContainerID,
  selectedMCPConnectionID,
  expandedServers,
  logs,
  logsLoading,
  logsError,
  autoScroll,
  onBack,
  onToggleServer,
  onSelectContainer,
  onSelectMCPConnection,
  onEditMCPConnection,
  onAutoScrollChange,
  onClearLogs,
  logsPanelRef,
  onMCPChanged,
}: {
  project: Project;
  servers: ServerWithNodelet[];
  serversLoading: boolean;
  serverError: string;
  selectedNodeletID: string;
  selectedContainerID: string;
  selectedMCPConnectionID?: string;
  expandedServers: Set<string>;
  logs: LogEntry[];
  logsLoading: boolean;
  logsError: string;
  autoScroll: boolean;
  onBack: () => void;
  onToggleServer: (id: string) => void;
  onSelectContainer: (nodeletID: string, containerID: string) => void;
  onSelectMCPConnection: (conn: ProjectMCPConnection) => void;
  onEditMCPConnection?: (conn: ProjectMCPConnection) => void;
  onAutoScrollChange: (v: boolean) => void;
  onClearLogs: () => void;
  logsPanelRef: React.RefObject<HTMLDivElement | null>;
  onMCPChanged: () => void;
}) {
  const nodeletAddress = servers.find((s) => s.nodelet.id === selectedNodeletID)?.nodelet.address;

  // Look up the selected MCP connection from cached data
  const { data: mcpConns = [] } = useProjectMCPConnections(project.id);
  const selectedMCPConnection = selectedMCPConnectionID
    ? mcpConns.find((c) => c.id === selectedMCPConnectionID)
    : undefined;

  // GitHub 仓库 inline 编辑状态。
  const [editingRepo, setEditingRepo] = React.useState(false);
  const [repoValue, setRepoValue] = React.useState(project.githubRepo || "");
  const [repoSaving, setRepoSaving] = React.useState(false);
  const [repoError, setRepoError] = React.useState("");
  const updateProject = useUpdateProject();

  React.useEffect(() => {
    setRepoValue(project.githubRepo || "");
  }, [project.githubRepo, project.id]);

  async function saveRepo() {
    if (repoSaving) return;
    setRepoSaving(true);
    setRepoError("");
    try {
      await updateProject.mutateAsync({
        id: project.id,
        name: project.name,
        description: project.description || "",
        githubRepo: repoValue.trim(),
      });
      setEditingRepo(false);
      // 通知父组件刷新。
      onMCPChanged();
    } catch (err) {
      setRepoError(getErrorMessage(err, "保存失败"));
    } finally {
      setRepoSaving(false);
    }
  }

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

        {/* GitHub 仓库信息 */}
        <div className="project-github-inline">
          {editingRepo ? (
            <>
              <FormInput
                value={repoValue}
                onChange={(e) => setRepoValue(e.target.value)}
                placeholder="https://github.com/user/repo"
                style={{ width: 260, fontSize: "var(--text-sm)" }}
                onKeyDown={(e) => { if (e.key === "Enter") saveRepo(); if (e.key === "Escape") { setEditingRepo(false); setRepoValue(project.githubRepo || ""); } }}
                autoFocus
              />
              <Button size="sm" onClick={saveRepo} disabled={repoSaving}>
                <Check size={14} />
              </Button>
              <Button size="sm" variant="ghost" onClick={() => { setEditingRepo(false); setRepoValue(project.githubRepo || ""); }}>
                <X size={14} />
              </Button>
              {repoError && <span className="error-text">{repoError}</span>}
            </>
          ) : project.githubRepo ? (
            <a href={project.githubRepo} target="_blank" rel="noopener noreferrer" className="project-github-inline-link">
              <Github size={14} />
              <span>{project.githubRepo.replace(/^https?:\/\/github\.com\//, "")}</span>
              <ExternalLink size={11} />
            </a>
          ) : (
            <button className="project-github-inline-cta" onClick={() => setEditingRepo(true)}>
              <Github size={14} />
              <span>设置仓库</span>
              <Pencil size={11} />
            </button>
          )}
        </div>
      </div>

      {/* 左右分栏 */}
      <div className="project-split">
        <ServerTree
          projectId={project.id}
          servers={servers}
          serversLoading={serversLoading}
          serverError={serverError}
          selectedNodeletID={selectedNodeletID}
          selectedContainerID={selectedContainerID}
          selectedMCPConnectionID={selectedMCPConnectionID}
          expandedServers={expandedServers}
          excludedContainerRefs={project.excludedContainerRefs || []}
          onToggleServer={onToggleServer}
          onSelectContainer={onSelectContainer}
          onSelectMCPConnection={onSelectMCPConnection}
        />

        <ContainerDetailView
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
          mcpConnection={selectedMCPConnection}
          onEditMCP={onEditMCPConnection}
          onMCPDeleted={() => {
            onMCPChanged();
          }}
          onNavigateToContainer={onSelectContainer}
          onMCPChanged={onMCPChanged}
        />
      </div>
    </div>
  );
}
