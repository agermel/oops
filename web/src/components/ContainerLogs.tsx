import { FileText } from "lucide-react";
import type { LogEntry } from "../types";
import { LogRow } from "./LogRow";

export function ContainerLogs({
  logs,
  loading,
  autoScroll,
  onAutoScrollChange,
  onClear,
  panelRef,
}: {
  logs: LogEntry[];
  loading: boolean;
  autoScroll: boolean;
  onAutoScrollChange: (checked: boolean) => void;
  onClear: () => void;
  panelRef: React.RefObject<HTMLDivElement | null>;
}) {
  return (
    <div className="container-logs">
      <div className="section-title logs-title">
        <span>
          <FileText size={18} />
          <h2>日志</h2>
        </span>
        <div className="log-actions">
          <label>
            <input type="checkbox" checked={autoScroll} onChange={(e) => onAutoScrollChange(e.target.checked)} />
            <span>自动滚动</span>
          </label>
          <button type="button" onClick={onClear} disabled={logs.length === 0}>
            清空
          </button>
        </div>
      </div>

      <div className="logs-panel" ref={panelRef}>
        {loading ? (
          <div className="empty-card">正在连接日志流</div>
        ) : logs.length === 0 ? (
          <div className="empty-card">等待实时日志</div>
        ) : (
          logs.map((entry, index) => <LogRow key={`${entry.timestamp}-${index}`} entry={entry} />)
        )}
      </div>
    </div>
  );
}
