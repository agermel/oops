import React from "react";
import { Plus, FolderKanban, Trash2, Edit3, ChevronRight } from "lucide-react";
import type { Project } from "../types";
import { apiRequest, getErrorMessage } from "../lib/api";
import { Modal } from "./Modal";

function emptyProject(): Project {
  return {
    id: "",
    name: "",
    description: "",
    nodeletIds: [],
    createdAt: "",
    updatedAt: "",
  };
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
  const [showForm, setShowForm] = React.useState(false);
  const [editing, setEditing] = React.useState<Project | null>(null);
  const [isNew, setIsNew] = React.useState(true);
  const [saving, setSaving] = React.useState(false);
  const [formError, setFormError] = React.useState("");

  function openAdd() {
    setIsNew(true);
    setEditing(emptyProject());
    setFormError("");
    setShowForm(true);
  }

  function openEdit(p: Project) {
    setIsNew(false);
    setEditing({ ...p });
    setFormError("");
    setShowForm(true);
  }

  function closeForm() {
    if (saving) return;
    setShowForm(false);
    setEditing(null);
    setFormError("");
  }

  async function handleSave() {
    if (!editing || saving) return;
    setSaving(true);
    setFormError("");
    const url = isNew ? "/api/projects" : `/api/projects/${encodeURIComponent(editing.id)}`;
    const method = isNew ? "POST" : "PUT";

    try {
      await apiRequest(url, {
        method,
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(editing),
      });
      closeForm();
      onRefresh();
    } catch (err) {
      setFormError(getErrorMessage(err, "保存失败"));
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete(id: string) {
    if (!window.confirm(`确定要删除该项目吗？`)) return;
    try {
      await apiRequest(`/api/projects/${encodeURIComponent(id)}`, { method: "DELETE" });
      onRefresh();
    } catch (err) {
      setFormError(getErrorMessage(err, "删除失败"));
    }
  }

  return (
    <section className="panel">
      <div className="panel-summary">
        <FolderKanban size={16} />
        <span>{loading ? "读取中" : `${projects.length} 个项目`}</span>
        <button className="primary-button small" onClick={openAdd}>
          <Plus size={15} />
          <span>新建项目</span>
        </button>
      </div>

      {error && <div className="error-line">{error}</div>}

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
                <small>{p.nodeletIds.length} 台服务器</small>
              </div>
              <ChevronRight size={20} className="project-card-arrow" />
            </div>
            <div className="project-card-actions">
              <button className="ghost-button small" aria-label="编辑项目" onClick={(e) => { e.stopPropagation(); openEdit(p); }}>
                <Edit3 size={14} />
              </button>
              <button className="ghost-button small danger" aria-label="删除项目" onClick={(e) => { e.stopPropagation(); handleDelete(p.id); }}>
                <Trash2 size={14} />
              </button>
            </div>
          </div>
        ))}
        {!loading && projects.length === 0 && (
          <div className="empty-card">暂无项目，点击"新建项目"开始</div>
        )}
      </div>

      {showForm && editing && (
        <Modal
          title={editing.id ? "编辑项目" : "新建项目"}
          onClose={closeForm}
          footer={
            <button className="primary-button" onClick={handleSave} disabled={!editing.name.trim() || saving}>
              {saving ? "保存中..." : "保存"}
            </button>
          }
        >
          <label htmlFor="project-name">名称</label>
          <input
            id="project-name"
            value={editing.name}
            onChange={(e) => setEditing({ ...editing, name: e.target.value, id: editing.id || e.target.value.toLowerCase().replace(/\s+/g, "-") })}
            placeholder="例如: CCNU Box"
          />
          <label htmlFor="project-desc">描述</label>
          <input
            id="project-desc"
            value={editing.description || ""}
            onChange={(e) => setEditing({ ...editing, description: e.target.value })}
            placeholder="项目简介（可选）"
          />
          {formError && <div className="error-line">{formError}</div>}
        </Modal>
      )}
    </section>
  );
}
