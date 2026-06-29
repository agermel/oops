import React from "react";
import { Terminal, Trash2, ChevronUp, ChevronDown } from "lucide-react";
import AnsiConvertor from "ansi-to-html";

interface ConsoleEntry {
  timestamp: string;
  level: "info" | "warn" | "error";
  message: string;
}

const ansi = new AnsiConvertor({ escapeXML: true, fg: "#f5f7fa", bg: "#1f2430" });

function sanitize(html: string): string {
  return html
    .replace(/<script[\s\S]*?<\/script>/gi, "")
    .replace(/<iframe[\s\S]*?<\/iframe>/gi, "")
    .replace(/<object[\s\S]*?<\/object>/gi, "")
    .replace(/<embed[\s\S]*?>/gi, "")
    .replace(/\bon\w+\s*=\s*"[^"]*"/gi, "")
    .replace(/\bon\w+\s*=\s*'[^']*'/gi, "")
    .replace(/javascript\s*:/gi, "");
}

const MAX_ENTRIES = 500;

export function ConsolePanel() {
  const [entries, setEntries] = React.useState<ConsoleEntry[]>([]);
  const [autoScroll, setAutoScroll] = React.useState(true);
  const [levelFilter, setLevelFilter] = React.useState<Set<string>>(() => new Set(["info", "warn", "error"]));
  const panelRef = React.useRef<HTMLDivElement>(null);
  const sourceRef = React.useRef<EventSource | null>(null);

  React.useEffect(() => {
    const source = new EventSource("/api/console/stream");
    sourceRef.current = source;

    source.onmessage = (event) => {
      try {
        const entry: ConsoleEntry = JSON.parse(event.data);
        setEntries((prev) => [...prev.slice(-(MAX_ENTRIES - 1)), entry]);
      } catch {
        // skip unparsable
      }
    };

    source.onerror = () => {
      // EventSource auto-reconnects
    };

    return () => {
      source.close();
    };
  }, []);

  React.useEffect(() => {
    if (autoScroll && panelRef.current) {
      panelRef.current.scrollTop = panelRef.current.scrollHeight;
    }
  }, [entries, autoScroll]);

  function toggleLevel(level: string) {
    setLevelFilter((prev) => {
      const next = new Set(prev);
      if (next.has(level)) next.delete(level);
      else next.add(level);
      return next;
    });
  }

  const filtered = entries.filter((e) => levelFilter.has(e.level));

  const errorCount = entries.filter((e) => e.level === "error").length;
  const warnCount = entries.filter((e) => e.level === "warn").length;

  return (
    <>
      <div className="workspace-head">
        <div>
          <h1>控制台</h1>
          <p>实时显示后端进程日志输出（stdout / stderr）。</p>
        </div>
      </div>

      {/* Toolbar */}
      <div className="console-toolbar">
        <div className="console-stats">
          {entries.length > 0 ? (
            <>
              <span className="console-stat">共 {entries.length} 条</span>
              {errorCount > 0 && <span className="console-stat error">{errorCount} err</span>}
              {warnCount > 0 && <span className="console-stat warn">{warnCount} warn</span>}
            </>
          ) : (
            <span className="console-stat">等待日志输出…</span>
          )}
        </div>
        <div className="console-filters">
          <button
            className={`console-filter-btn${levelFilter.has("info") ? " active" : ""}`}
            onClick={() => toggleLevel("info")}
          >
            info
          </button>
          <button
            className={`console-filter-btn${levelFilter.has("warn") ? " active" : ""}`}
            onClick={() => toggleLevel("warn")}
          >
            warn
          </button>
          <button
            className={`console-filter-btn${levelFilter.has("error") ? " active" : ""}`}
            onClick={() => toggleLevel("error")}
          >
            error
          </button>
          <button
            className={`console-filter-btn${autoScroll ? " active" : ""}`}
            onClick={() => setAutoScroll(!autoScroll)}
            title={autoScroll ? "自动滚动 开" : "自动滚动 关"}
          >
            {autoScroll ? <ChevronDown size={12} /> : <ChevronUp size={12} />}
          </button>
          <button className="console-filter-btn" onClick={() => setEntries([])} title="清空">
            <Trash2 size={12} />
          </button>
        </div>
      </div>

      {/* Log body */}
      <div className="console-body-panel" ref={panelRef}>
        {filtered.length === 0 ? (
          <div className="console-empty">暂无日志输出，等待后端推送…</div>
        ) : (
          filtered.map((e, i) => (
            <div key={i} className={`console-line level-${e.level}`}>
              <span className="console-ts">{e.timestamp.slice(11, 19)}</span>
              <span className="console-level-tag">{e.level}</span>
              <span className="console-msg" dangerouslySetInnerHTML={{ __html: sanitize(ansi.toHtml(e.message)) }} />
            </div>
          ))
        )}
      </div>
    </>
  );
}
