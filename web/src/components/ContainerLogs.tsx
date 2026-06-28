import React from "react";
import { FileText, ChevronDown } from "lucide-react";
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
  // 1px 哨兵元素 —— IntersectionObserver 观察它是否在视口中
  const sentinelRef = React.useRef<HTMLDivElement | null>(null);
  // 是否有新日志在底部之外（用户上翻后）
  const [hasMore, setHasMore] = React.useState(false);

  // ---- IntersectionObserver：哨兵可见 → 用户在底部 ----
  React.useEffect(() => {
    const sentinel = sentinelRef.current;
    const panel = panelRef.current;
    if (!sentinel || !panel) return;

    const observer = new IntersectionObserver(
      ([entry]) => {
        // 哨兵可见（即使部分可见）→ 用户在底部附近 → 打开自动滚动
        if (entry.isIntersecting) {
          onAutoScrollChange(true);
          setHasMore(false);
        } else {
          // 哨兵完全不可见 → 用户已上翻
          onAutoScrollChange(false);
        }
      },
      {
        root: panel,
        threshold: [0, 1],
        rootMargin: "40px 0px",
      }
    );

    observer.observe(sentinel);
    return () => observer.disconnect();
  }, [panelRef, onAutoScrollChange]);

  // ---- MutationObserver：新内容追加时，自动滚到底部或显示"回到底部" ----
  React.useEffect(() => {
    const panel = panelRef.current;
    if (!panel) return;

    const observer = new MutationObserver(() => {
      if (autoScroll) {
        // scrollIntoView 比 scrollTop=scrollHeight 更可靠
        sentinelRef.current?.scrollIntoView({ behavior: "auto" });
      } else {
        // 用户在翻阅历史日志，提示有新内容
        setHasMore(true);
      }
    });

    observer.observe(panel, { childList: true, subtree: true });
    return () => observer.disconnect();
  }, [panelRef, autoScroll]);

  // 首次有日志时滚到底部
  const didInitialScroll = React.useRef(false);
  React.useEffect(() => {
    if (!didInitialScroll.current && logs.length > 0 && panelRef.current) {
      sentinelRef.current?.scrollIntoView({ behavior: "auto" });
      didInitialScroll.current = true;
    }
  }, [logs.length, panelRef]);

  // 切换容器时重置
  React.useEffect(() => {
    didInitialScroll.current = false;
    setHasMore(false);
  }, [logs]);

  function scrollToBottom() {
    sentinelRef.current?.scrollIntoView({ behavior: "smooth" });
    onAutoScrollChange(true);
    setHasMore(false);
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

      <div className="logs-panel" ref={panelRef} role="log" aria-live="polite">
        {error ? (
          <div className="error-line">{error}</div>
        ) : logs.length === 0 ? (
          <div className="empty-card">{loading ? "正在连接日志流" : "等待实时日志"}</div>
        ) : (
          logs.map((entry, index) => <LogRow key={`${entry.timestamp}-${index}`} entry={entry} />)
        )}
        {/* 哨兵：始终在日志列表末尾，用于检测用户是否在底部 */}
        <div ref={sentinelRef} className="logs-sentinel" />
      </div>

      {/* 回到底部浮动按钮 */}
      {hasMore && (
        <button type="button" className="logs-scroll-bottom" onClick={scrollToBottom} aria-label="回到底部">
          <ChevronDown size={18} />
        </button>
      )}
    </div>
  );
}
