import React from "react";
import { Skill } from "../types";
import { apiRequest, getErrorMessage } from "../lib/api";
import { Pencil, Trash2, Eye, EyeOff } from "lucide-react";

type Props = {
  skills: Skill[];
  onRefresh: () => void;
};

export function SkillsView({ skills, onRefresh }: Props) {
  const [editing, setEditing] = React.useState<Skill | null>(null);
  const [saving, setSaving] = React.useState(false);
  const [saveError, setSaveError] = React.useState("");
  const [deleting, setDeleting] = React.useState<string | null>(null);

  async function handleSave(updated: Skill) {
    setSaving(true);
    setSaveError("");
    try {
      await apiRequest(`/api/skills/${encodeURIComponent(updated.name)}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(updated),
      });
      setEditing(null);
      onRefresh();
    } catch (err) {
      setSaveError(getErrorMessage(err, "保存失败"));
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete(name: string) {
    if (!window.confirm(`确定要删除技能 "${name}" 吗？`)) return;
    setDeleting(name);
    try {
      await apiRequest(`/api/skills/${encodeURIComponent(name)}`, { method: "DELETE" });
      onRefresh();
    } catch (err) {
      alert(getErrorMessage(err, "删除失败"));
    } finally {
      setDeleting(null);
    }
  }

  return (
    <div className="mcp-table-wrap">
      <table className="mcp-table">
        <thead>
          <tr>
            <th>名称</th>
            <th>标签</th>
            <th>描述</th>
            <th>最大步数</th>
            <th>状态</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          {skills.length === 0 && (
            <tr>
              <td colSpan={6} className="empty-cell">暂无技能</td>
            </tr>
          )}
          {skills.map((skill) => (
            <tr key={skill.name}>
              <td className="mono">{skill.name}</td>
              <td>{skill.label}</td>
              <td className="desc-cell" title={skill.description}>{skill.description}</td>
              <td>{skill.name === "diagnose" ? 10 : skill.name === "inspect" ? 5 : 15}</td>
              <td>
                <span className={`status-pill ${skill.enabled ? "pill-alive" : "pill-dead"}`}>
                  {skill.enabled ? "启用" : "禁用"}
                </span>
              </td>
              <td className="actions-cell">
                <button
                  className="btn btn-sm btn-ghost"
                  title="编辑"
                  onClick={() => setEditing(skill)}
                >
                  <Pencil size={14} />
                </button>
                <button
                  className="btn btn-sm btn-ghost"
                  title="删除"
                  disabled={deleting === skill.name || skill.name === "default"}
                  onClick={() => handleDelete(skill.name)}
                >
                  <Trash2 size={14} />
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      {editing && (
        <SkillsEditModal
          skill={editing}
          saving={saving}
          saveError={saveError}
          onSave={handleSave}
          onClose={() => { setEditing(null); setSaveError(""); }}
        />
      )}
    </div>
  );
}

function SkillsEditModal({
  skill,
  saving,
  saveError,
  onSave,
  onClose,
}: {
  skill: Skill;
  saving: boolean;
  saveError: string;
  onSave: (s: Skill) => void;
  onClose: () => void;
}) {
  const [form, setForm] = React.useState({ ...skill });

  function update<K extends keyof Skill>(key: K, value: Skill[K]) {
    setForm((prev) => ({ ...prev, [key]: value }));
  }

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal" onClick={(e) => e.stopPropagation()}>
        <div className="modal-header">
          <h3>编辑技能: {skill.name}</h3>
          <button className="btn btn-sm btn-ghost" onClick={onClose}>✕</button>
        </div>
        <div className="modal-body">
          <label className="form-label">
            标签
            <input
              className="form-input"
              value={form.label}
              onChange={(e) => update("label", e.target.value)}
            />
          </label>
          <label className="form-label">
            图标 (Lucide 图标名)
            <input
              className="form-input"
              value={form.icon}
              onChange={(e) => update("icon", e.target.value)}
            />
          </label>
          <label className="form-label">
            颜色主题
            <select
              className="form-input"
              value={form.color}
              onChange={(e) => update("color", e.target.value)}
            >
              <option value="diagnose">diagnose (诊断-红)</option>
              <option value="inspect">inspect (巡检-绿)</option>
              <option value="default">default (通用-蓝)</option>
              <option value="custom">custom (自定义)</option>
            </select>
          </label>
          <label className="form-label">
            描述
            <input
              className="form-input"
              value={form.description}
              onChange={(e) => update("description", e.target.value)}
            />
          </label>
          <label className="form-label">
            状态
            <select
              className="form-input"
              value={form.enabled ? "enabled" : "disabled"}
              onChange={(e) => update("enabled", e.target.value === "enabled")}
            >
              <option value="enabled">启用</option>
              <option value="disabled">禁用</option>
            </select>
          </label>
          <label className="form-label">
            内容 (Markdown)
            <textarea
              className="form-textarea"
              rows={15}
              value={form.content}
              onChange={(e) => update("content", e.target.value)}
            />
          </label>
          {saveError && <div className="error-banner">{saveError}</div>}
        </div>
        <div className="modal-footer">
          <button className="btn btn-ghost" onClick={onClose} disabled={saving}>取消</button>
          <button className="btn btn-primary" onClick={() => onSave(form)} disabled={saving}>
            {saving ? "保存中..." : "保存"}
          </button>
        </div>
      </div>
    </div>
  );
}
