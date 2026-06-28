import { FileText } from "lucide-react";
import type { LogEntry } from "../types";
import { LogRow } from "./LogRow";

export function ContainerLogs({
  logs,
  loading,
  error,
  autoScroll,
  onAutoScrollChange,
  onClear,
  panelRef,
}: {
  logs: LogEntry[];
  loading: boolean;
  error: string;
  autoScroll: boolean;
  onAutoScrollChange: (checked: boolean) => void;
  onClear: () => void;
  panelRef: React.RefObject<HTMLDivElement | null>;
}) {
  function handleScroll() {
    if (!panelRef.current) return;
    const { scrollTop, scrollHeight, clientHeight } = panelRef.current;
    // 用户上翻超过一行时自动关闭自动滚动。
    if (scrollHeight - scrollTop - clientHeight > 40) {
      onAutoScrollChange(false);
    }
  }

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
          <button type="button" onClick={onClear} disabled={logs.length === 0} aria-label="清空日志">
            清空
          </button>
        </div>
      </div>

      <div className="logs-panel" ref={panelRef} role="log" aria-live="polite" onScroll={handleScroll}>
        {error ? (
          <div className="error-line">{error}</div>
        ) : loading ? (
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
