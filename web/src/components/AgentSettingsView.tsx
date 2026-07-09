import React from "react";
import { RotateCcw, Save, SlidersHorizontal } from "lucide-react";
import { getErrorMessage } from "../lib/api";
import { useAgentSettings, useUpdateAgentSettings } from "../hooks/useAgentSettings";
import { Button } from "./ui/Button";

const minMaxTurns = 1;
const maxMaxTurns = 100;

export function AgentSettingsView() {
  const { data, isLoading, error: queryError, refetch } = useAgentSettings();
  const updateSettings = useUpdateAgentSettings();
  const [maxTurnsDraft, setMaxTurnsDraft] = React.useState("15");

  React.useEffect(() => {
    if (data) setMaxTurnsDraft(String(data.maxTurns));
  }, [data]);

  const error = queryError ? getErrorMessage(queryError, "读取设置失败") : "";
  const saveError = updateSettings.error ? getErrorMessage(updateSettings.error, "保存设置失败") : "";
  const trimmedMaxTurns = maxTurnsDraft.trim();
  const parsedMaxTurns = Number(trimmedMaxTurns);
  const valid =
    /^\d+$/.test(trimmedMaxTurns) && parsedMaxTurns >= minMaxTurns && parsedMaxTurns <= maxMaxTurns;
  const dirty = data ? trimmedMaxTurns !== String(data.maxTurns) : false;

  function handleInputChange(event: React.ChangeEvent<HTMLInputElement>) {
    setMaxTurnsDraft(event.target.value);
  }

  function handleReset() {
    if (data) setMaxTurnsDraft(String(data.maxTurns));
  }

  function handleSave() {
    if (!valid) return;
    updateSettings.mutate({ maxTurns: parsedMaxTurns });
  }

  return (
    <section className="panel agent-settings-panel">
      <div className="panel-summary">
        <SlidersHorizontal size={16} />
        <span>{isLoading ? "读取中" : "Agent Runtime"}</span>
        <Button variant="ghost" size="sm" onClick={() => refetch()} disabled={isLoading}>
          <RotateCcw size={14} className={isLoading ? "spin" : ""} />
          <span>刷新</span>
        </Button>
      </div>

      {error && <div className="error-banner">{error}</div>}
      {saveError && <div className="error-banner">{saveError}</div>}

      <div className="settings-grid">
        <label className="settings-field">
          <span className="settings-label">Max Turns</span>
          <input
            className="form-input"
            type="number"
            min={minMaxTurns}
            max={maxMaxTurns}
            step={1}
            value={maxTurnsDraft}
            onChange={handleInputChange}
            disabled={isLoading || updateSettings.isPending}
          />
        </label>
        <div className="settings-meta">
          <span>范围 {minMaxTurns} - {maxMaxTurns}</span>
          <span>当前保存值 {data?.maxTurns ?? "—"}</span>
        </div>
      </div>

      {!valid && <div className="form-error">Max Turns 必须在 {minMaxTurns} 到 {maxMaxTurns} 之间。</div>}

      <div className="settings-actions">
        <Button variant="ghost" onClick={handleReset} disabled={!dirty || updateSettings.isPending}>
          重置
        </Button>
        <Button onClick={handleSave} disabled={!dirty || !valid || updateSettings.isPending}>
          <Save size={15} />
          <span>{updateSettings.isPending ? "保存中" : "保存"}</span>
        </Button>
      </div>
    </section>
  );
}
