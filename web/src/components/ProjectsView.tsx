import React from "react";
import { Plus, FolderKanban, Trash2, Edit3, ChevronRight, X } from "lucide-react";
import type { Project } from "../types";

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

  function openAdd() {
    setIsNew(true);
    setEditing(emptyProject());
    setShowForm(true);
  }

  function openEdit(p: Project) {
    setIsNew(false);
    setEditing({ ...p });
    setShowForm(true);
  }

  function closeForm() {
    setShowForm(false);
    setEditing(null);
  }

  async function handleSave() {
    if (!editing) return;
    const url = isNew ? "/api/projects" : `/api/projects/${encodeURIComponent(editing.id)}`;
    const method = isNew ? "POST" : "PUT";

    try {
      const resp = await fetch(url, {
        method,
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(editing),
      });
      if (!resp.ok) {
        const data = await resp.json().catch(() => ({ error: `HTTP ${resp.status}` }));
        throw new Error(data.error || `HTTP ${resp.status}`);
      }
      closeForm();
      onRefresh();
    } catch (err) {
      alert(err instanceof Error ? err.message : "保存失败");
    }
  }

  async function handleDelete(id: string) {
    if (!window.confirm(`确定要删除该项目吗？`)) return;
    try {
      const resp = await fetch(`/api/projects/${encodeURIComponent(id)}`, { method: "DELETE" });
      if (!resp.ok) {
        const data = await resp.json().catch(() => ({ error: `HTTP ${resp.status}` }));
        throw new Error(data.error || `HTTP ${resp.status}`);
      }
      onRefresh();
    } catch (err) {
      alert(err instanceof Error ? err.message : "删除失败");
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
              <button className="ghost-button small" onClick={(e) => { e.stopPropagation(); openEdit(p); }}>
                <Edit3 size={14} />
              </button>
              <button className="ghost-button small danger" onClick={(e) => { e.stopPropagation(); handleDelete(p.id); }}>
                <Trash2 size={14} />
              </button>
            </div>
          </div>
        ))}
        {!loading && projects.length === 0 && (
          <div className="empty-card">暂无项目，点击"新建项目"开始</div>
        )}
      </div>

      {/* Modal */}
      {showForm && editing && (
        <div className="modal-overlay" onClick={closeForm}>
          <div className="modal-card" onClick={(e) => e.stopPropagation()}>
            <div className="modal-head">
              <h2>{editing.id ? "编辑项目" : "新建项目"}</h2>
              <button className="ghost-button" onClick={closeForm}><X size={18} /></button>
            </div>
            <div className="modal-body">
              <label>名称</label>
              <input
                value={editing.name}
                onChange={(e) => setEditing({ ...editing, name: e.target.value, id: editing.id || e.target.value.toLowerCase().replace(/\s+/g, "-") })}
                placeholder="例如: CCNU Box"
              />
              <label>描述</label>
              <input
                value={editing.description || ""}
                onChange={(e) => setEditing({ ...editing, description: e.target.value })}
                placeholder="项目简介（可选）"
              />
            </div>
            <div className="modal-foot">
              <button className="primary-button" onClick={handleSave} disabled={!editing.name.trim()}>
                保存
              </button>
            </div>
          </div>
        </div>
      )}
    </section>
  );
}
