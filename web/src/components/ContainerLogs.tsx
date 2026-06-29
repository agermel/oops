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
  // 跟踪用户是否手动关闭了自动滚动（避免 IntersectionObserver 反复切换）
  const userScrolledUpRef = React.useRef(false);

  // ---- IntersectionObserver：哨兵可见 → 用户在底部 ----
  React.useEffect(() => {
    const sentinel = sentinelRef.current;
    const panel = panelRef.current;
    if (!sentinel || !panel) return;

    const observer = new IntersectionObserver(
      ([entry]) => {
        // 哨兵可见（即使部分可见）→ 用户在底部附近 → 打开自动滚动
        if (entry.isIntersecting) {
          if (userScrolledUpRef.current) {
            // 用户之前翻上去，现在回来了 → 恢复自动滚动
            userScrolledUpRef.current = false;
          }
          onAutoScrollChange(true);
          setHasMore(false);
        } else {
          // 哨兵完全不可见 → 用户已上翻
          userScrolledUpRef.current = true;
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
  // 使用 ref 持有最新 autoScroll 值，避免 MutationObserver 因 autoScroll 变化而反复重建
  const autoScrollRef = React.useRef(autoScroll);
  autoScrollRef.current = autoScroll;

  React.useEffect(() => {
    const panel = panelRef.current;
    if (!panel) return;

    const observer = new MutationObserver(() => {
      if (autoScrollRef.current) {
        sentinelRef.current?.scrollIntoView({ behavior: "auto" });
      } else {
        setHasMore(true);
      }
    });

    observer.observe(panel, { childList: true, subtree: true });
    return () => observer.disconnect();
  }, [panelRef]);

  // 首次有日志时滚到底部
  const didInitialScroll = React.useRef(false);
  React.useEffect(() => {
    if (!didInitialScroll.current && logs.length > 0 && panelRef.current) {
      sentinelRef.current?.scrollIntoView({ behavior: "auto" });
      didInitialScroll.current = true;
    }
  }, [logs.length, panelRef]);

  // 切换容器日志源时重置
  React.useEffect(() => {
    didInitialScroll.current = false;
    setHasMore(false);
    userScrolledUpRef.current = false;
  // eslint-disable-next-line react-hooks/exhaustive-deps -- 仅在 logs 引用变化时重置（新容器 = 新数组引用）
  }, [logs]);

  function scrollToBottom() {
    sentinelRef.current?.scrollIntoView({ behavior: "smooth" });
    onAutoScrollChange(true);
    setHasMore(false);
  }

  return (
    <div className="container-logs">
      <div className="section-header section-header-spread">
        <span style={{ display: "inline-flex", alignItems: "center", gap: "var(--space-2)" }}>
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
          <div className="error-banner">{error}</div>
        ) : logs.length === 0 ? (
          <div className="empty-state">{loading ? "正在连接日志流" : "等待实时日志"}</div>
        ) : (
          logs.map((entry, index) => <LogRow key={`${entry.timestamp || "0"}-${index}-${entry.containerId || ""}`} entry={entry} />)
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
