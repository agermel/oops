import React from "react";
import { Settings, Plus, Trash2, RotateCcw } from "lucide-react";
import type { DSNConfig, DSNInfo } from "../types";
import { apiRequest, getErrorMessage } from "../lib/api";
import { serverPaths } from "../lib/paths";
import { DSNInfoCard } from "./DSNInfoCard";
import { Button } from "./ui/Button";
import { FormInput } from "./ui/FormInput";

function mergedToDSNInfo(merged: Record<string, string>, detected: Record<string, string>): DSNInfo {
  return {
    host: merged.host || detected.host || "",
    port: parseInt(merged.port || detected.port || "0", 10) || 0,
    user: merged.user || detected.user,
    database: merged.database || detected.database,
    raw: merged.raw || detected.raw,
  };
}

type KVRow = { key: string; value: string; id: number };

let nextID = 0;
function newRow(key?: string, value?: string): KVRow {
  return { id: ++nextID, key: key || "", value: value || "" };
}

function pairsToRecord(rows: KVRow[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (const r of rows) {
    if (r.key.trim()) {
      out[r.key.trim()] = r.value;
    }
  }
  return out;
}

function recordToRows(rec: Record<string, string> | undefined | null): KVRow[] {
  if (!rec || Object.keys(rec).length === 0) return [newRow()];
  return Object.entries(rec).map(([k, v]) => newRow(k, v));
}

export function ContainerDSN({
  projectId,
  nodeletId,
  containerId,
  serviceType,
  onChanged,
}: {
  projectId: string;
  nodeletId: string;
  containerId: string;
  serviceType: string;
  onChanged?: () => void;
}) {
  const paths = serverPaths(projectId, nodeletId);
  const dsnURL = paths.containerDSN(containerId);

  const [dsnConfig, setDsnConfig] = React.useState<DSNConfig | null>(null);
  const [rows, setRows] = React.useState<KVRow[]>([newRow()]);
  const [loading, setLoading] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [error, setError] = React.useState("");
  const [savedMsg, setSavedMsg] = React.useState("");

  // 判断是否有未保存修改：比较当前 rows 和 merged
  const dirty = React.useMemo(() => {
    if (!dsnConfig || !dsnConfig.merged) return false;
    const current = pairsToRecord(rows);
    const orig = dsnConfig.merged;
    const allKeys = new Set([...Object.keys(current), ...Object.keys(orig)]);
    for (const k of allKeys) {
      if ((current[k] || "") !== (orig[k] || "")) return true;
    }
    return false;
  }, [rows, dsnConfig]);

  async function fetchDSN() {
    setLoading(true);
    setError("");
    setSavedMsg("");
    try {
      const data = await apiRequest<DSNConfig>(dsnURL);
      setDsnConfig(data);
      setRows(recordToRows(data.merged));
    } catch (err) {
      setError(getErrorMessage(err, "读取 DSN 配置失败"));
    } finally {
      setLoading(false);
    }
  }

  // 挂载时获取 + containerId 变化时重新获取
  React.useEffect(() => {
    fetchDSN();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [containerId]);

  function updateRow(id: number, field: "key" | "value", val: string) {
    setRows((prev) => prev.map((r) => (r.id === id ? { ...r, [field]: val } : r)));
    setSavedMsg("");
  }

  function addRow() {
    setRows((prev) => [...prev, newRow()]);
    setSavedMsg("");
  }

  function removeRow(id: number) {
    setRows((prev) => {
      if (prev.length <= 1) return prev; // keep at least one row
      return prev.filter((r) => r.id !== id);
    });
    setSavedMsg("");
  }

  async function handleSave() {
    setSaving(true);
    setError("");
    setSavedMsg("");
    try {
      await apiRequest(dsnURL, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ pairs: pairsToRecord(rows) }),
      });
      // 重新获取以同步 merged/overrides/hasOverrides
      await fetchDSN();
      setSavedMsg("已保存");
      onChanged?.();
    } catch (err) {
      setError(getErrorMessage(err, "保存 DSN 配置失败"));
    } finally {
      setSaving(false);
    }
  }

  async function handleRestore() {
    setSaving(true);
    setError("");
    setSavedMsg("");
    try {
      await apiRequest(dsnURL, { method: "DELETE" });
      await fetchDSN();
      setSavedMsg("已恢复为检测值");
      onChanged?.();
    } catch (err) {
      setError(getErrorMessage(err, "恢复 DSN 检测值失败"));
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="container-dsn">
      <div className="section-header">
        <Settings size={18} />
        <h2>DSN 配置</h2>
      </div>

      {error && <div className="error-banner">{error}</div>}
      {savedMsg && <div className="success-banner">{savedMsg}</div>}

      {loading ? (
        <div className="empty-state">正在加载 DSN 配置</div>
      ) : (
        <>
          {/* 实时预览 */}
          {dsnConfig && (
            <DSNInfoCard
              dsn={mergedToDSNInfo(dsnConfig.merged, dsnConfig.detected)}
              dsnOverrides={dsnConfig.overrides}
              hasDSNOverrides={dsnConfig.hasOverrides}
            />
          )}

          <p className="dsn-hint">
            {dsnConfig && dsnConfig.detected && Object.keys(dsnConfig.detected).length > 0
              ? `已自动检测 ${serviceType} 连接信息，您可以覆盖或添加新的键值对。`
              : `当前容器（${serviceType}）无自动检测的 DSN 信息，请手动添加连接参数。`}
          </p>

          <div className="dsn-kv-table-wrap">
            <table className="dsn-kv-table">
              <colgroup>
                <col className="dsn-kv-col-key" />
                <col className="dsn-kv-col-value" />
                <col className="dsn-kv-col-del" />
              </colgroup>
              <thead>
                <tr>
                  <th>键</th>
                  <th>值</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                {rows.map((row) => (
                  <tr key={row.id}>
                    <td>
                      <FormInput
                        value={row.key}
                        onChange={(e) => updateRow(row.id, "key", e.target.value)}
                        placeholder="host"
                        spellCheck={false}
                      />
                    </td>
                    <td>
                      <FormInput
                        value={row.value}
                        onChange={(e) => updateRow(row.id, "value", e.target.value)}
                        placeholder="127.0.0.1"
                        spellCheck={false}
                      />
                    </td>
                    <td>
                      <Button
                        variant="ghost"
                        size="sm"
                        danger
                        aria-label="删除此键值对"
                        onClick={() => removeRow(row.id)}
                        disabled={rows.length <= 1}
                      >
                        <Trash2 size={14} />
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <div className="dsn-kv-actions">
            <Button variant="ghost" size="sm" onClick={addRow}>
              <Plus size={14} />
              <span>添加</span>
            </Button>
            <div className="dsn-kv-actions-right">
              {dsnConfig?.hasOverrides && (
                <Button variant="ghost" size="sm" onClick={handleRestore} disabled={saving}>
                  <RotateCcw size={14} />
                  <span>恢复检测值</span>
                </Button>
              )}
              <Button
                size="sm"
                onClick={handleSave}
                disabled={saving}
                className={dirty ? "" : "button-muted"}
              >
                {saving ? "保存中..." : "保存"}
              </Button>
            </div>
          </div>
        </>
      )}
    </div>
  );
}
