import React from "react";
import { Plus, FolderKanban, Trash2, Edit3, ChevronRight, Github } from "lucide-react";
import type { Project } from "../types";
import { useModal } from "../hooks/useModal";
import { apiRequest, getErrorMessage } from "../lib/api";
import { projectBasePaths, projectPaths } from "../lib/paths";
import { Modal } from "./Modal";
import { Button } from "./ui/Button";
import { FormInput } from "./ui/FormInput";

function emptyProject(): Project {
  return { id: "", name: "", description: "", githubRepo: "", nodeletIds: [], createdAt: "", updatedAt: "" };
}

function savePayload(project: Project) {
  return { id: project.id, name: project.name, description: project.description || "", githubRepo: project.githubRepo || "" };
}

export function ProjectsView({
  projects,
  loading,
  error,
  onSelect,
  onRefresh,
}: {
  projects: Project[];
  loading: boolean;
  error: string;
  onSelect: (id: string) => void;
  onRefresh: () => void;
}) {
  const [saving, setSaving] = React.useState(false);
  const [formError, setFormError] = React.useState("");
  const [deleteError, setDeleteError] = React.useState("");
  const githubInputRef = React.useRef<HTMLInputElement>(null);

  const formModal = useModal<Project>();
  const isNew = formModal.data ? formModal.data.id === "" : true;

  function openAdd() {
    setFormError("");
    formModal.onOpen(emptyProject());
  }

  function openEdit(p: Project, focusGithub?: boolean) {
    setFormError("");
    formModal.onOpen({ ...p });
    if (focusGithub) {
      setTimeout(() => githubInputRef.current?.focus(), 50);
    }
  }

  function closeForm() {
    if (saving) return;
    formModal.onClose();
    setFormError("");
  }

  async function handleSave() {
    if (!formModal.data || saving) return;
    setSaving(true);
    setFormError("");
    const url = isNew ? projectBasePaths.create : projectPaths(formModal.data!.id).detail;
    const method = isNew ? "POST" : "PUT";

    try {
      const created = await apiRequest<Project>(url, {
        method,
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(savePayload(formModal.data!)),
      });
      closeForm();
      if (isNew && created?.id) {
        onSelect(created.id);
      }
      onRefresh();
    } catch (err) {
      setFormError(getErrorMessage(err, "保存失败"));
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete(id: string) {
    if (!window.confirm(`确定要删除该项目吗？`)) return;
    setDeleteError("");
    try {
      await apiRequest(projectPaths(id).detail, { method: "DELETE" });
      onRefresh();
    } catch (err) {
      setDeleteError(getErrorMessage(err, "删除失败"));
    }
  }

  return (
    <section className="panel">
      <div className="panel-summary">
        <FolderKanban size={16} />
        <span>{loading ? "读取中" : `${projects.length} 个项目`}</span>
        <Button size="sm" onClick={openAdd}>
          <Plus size={15} />
          <span>新建项目</span>
        </Button>
      </div>

      {error && <div className="error-banner">{error}</div>}
      {deleteError && <div className="error-banner">{deleteError}</div>}

      <div className="project-grid">
        {projects.map((p) => (
          <div key={p.id} className="project-card">
            <div className="project-card-body" onClick={() => onSelect(p.id)}>
              <div className="project-card-icon">
                <FolderKanban size={28} />
              </div>
              <div className="project-card-info">
                <h3>{p.name}</h3>
                {p.description && <p>{p.description}</p>}
                <div className="project-card-meta">
                  {p.githubRepo ? (
                    <a
                      href={p.githubRepo}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="project-github-link"
                      onClick={(e) => e.stopPropagation()}
                    >
                      <Github size={14} />
                      <span>{p.githubRepo.replace(/^https?:\/\/github\.com\//, "")}</span>
                    </a>
                  ) : (
                    <button
                      className="project-github-cta"
                      onClick={(e) => { e.stopPropagation(); openEdit(p, true); }}
                    >
                      <Github size={14} />
                      <span>设置 GitHub 仓库</span>
                    </button>
                  )}
                  <small>{p.nodeletIds.length} 台服务器</small>
                </div>
              </div>
              <ChevronRight size={20} className="project-card-arrow" />
            </div>
            <div className="project-card-actions">
              <Button variant="ghost" size="sm" iconOnly aria-label="编辑项目" onClick={(e) => { e.stopPropagation(); openEdit(p); }}>
                <Edit3 size={14} />
              </Button>
              <Button variant="ghost" size="sm" iconOnly danger aria-label="删除项目" onClick={(e) => { e.stopPropagation(); handleDelete(p.id); }}>
                <Trash2 size={14} />
              </Button>
            </div>
          </div>
        ))}
        {!loading && projects.length === 0 && (
          <div className="empty-state">暂无项目，点击"新建项目"开始</div>
        )}
      </div>

      {formModal.open && formModal.data && (
        <Modal
          title={formModal.data.id ? "编辑项目" : "新建项目"}
          onClose={closeForm}
          footer={
            <Button onClick={handleSave} disabled={!formModal.data.name.trim() || saving}>
              {saving ? "保存中..." : "保存"}
            </Button>
          }
        >
          <label htmlFor="project-name">名称</label>
          <FormInput
            id="project-name"
            value={formModal.data.name}
            onChange={(e) => formModal.setData({ ...formModal.data!, name: e.target.value, id: formModal.data!.id || `${e.target.value.toLowerCase().replace(/\s+/g, "-")}-${Date.now()}` })}
            placeholder="例如: CCNU Box"
          />
          <label htmlFor="project-desc">描述</label>
          <FormInput
            id="project-desc"
            value={formModal.data.description || ""}
            onChange={(e) => formModal.setData({ ...formModal.data!, description: e.target.value })}
            placeholder="项目简介（可选）"
          />
          <label htmlFor="project-github">GitHub 仓库</label>
          <FormInput
            id="project-github"
            ref={githubInputRef}
            value={formModal.data.githubRepo || ""}
            onChange={(e) => formModal.setData({ ...formModal.data!, githubRepo: e.target.value })}
            placeholder="例如: https://github.com/user/repo"
          />
          {formError && <div className="error-banner">{formError}</div>}
        </Modal>
      )}
    </section>
  );
}
