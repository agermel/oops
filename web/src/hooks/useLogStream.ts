import React from "react";
import type { LogEntry } from "../types";
import { MAX_LOGS, LOG_FLUSH_MS, LOG_MAX_WAIT_MS } from "../types";
import { serverPaths } from "../lib/paths";

export function useLogStream(projectId: string, nodeletId: string, containerId: string) {
  const [logs, setLogs] = React.useState<LogEntry[]>([]);
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState("");
  const [autoScroll, setAutoScroll] = React.useState(true);
  const panelRef = React.useRef<HTMLDivElement | null>(null);

  const sourceRef = React.useRef<EventSource | null>(null);
  const bufferRef = React.useRef<LogEntry[]>([]);
  const flushTimerRef = React.useRef<ReturnType<typeof setTimeout> | null>(null);
  const flushFirstRef = React.useRef<number>(0);

  // ---- 缓冲区刷新 ----
  const doFlushRef = React.useRef(() => {
    if (bufferRef.current.length === 0) return;
    const next = bufferRef.current;
    bufferRef.current = [];
    setLogs((prev) => [...prev, ...next].slice(-MAX_LOGS));
  });
  doFlushRef.current = () => {
    if (bufferRef.current.length === 0) return;
    const next = bufferRef.current;
    bufferRef.current = [];
    setLogs((prev) => [...prev, ...next].slice(-MAX_LOGS));
  };

  const scheduleFlush = React.useCallback(() => {
    const now = Date.now();
    if (flushFirstRef.current === 0) flushFirstRef.current = now;
    if (now - flushFirstRef.current >= LOG_MAX_WAIT_MS) {
      if (flushTimerRef.current) { clearTimeout(flushTimerRef.current); flushTimerRef.current = null; }
      flushFirstRef.current = 0;
      doFlushRef.current();
      return;
    }
    if (flushTimerRef.current) clearTimeout(flushTimerRef.current);
    flushTimerRef.current = setTimeout(() => {
      flushTimerRef.current = null;
      flushFirstRef.current = 0;
      doFlushRef.current();
    }, LOG_FLUSH_MS);
  }, []);

  // ---- 流生命周期 ----
  const activeRef = React.useRef("");
  React.useEffect(() => {
    const target = `${nodeletId}/${containerId}`;
    if (!containerId) {
      // 没有选中容器 → 关闭
      if (sourceRef.current) { sourceRef.current.close(); sourceRef.current = null; }
      if (flushTimerRef.current) { clearTimeout(flushTimerRef.current); flushTimerRef.current = null; }
      flushFirstRef.current = 0;
      bufferRef.current = [];
      activeRef.current = "";
      return;
    }
    if (activeRef.current === target) return; // 同一个容器
    activeRef.current = target;

    // 关闭旧流
    if (sourceRef.current) { sourceRef.current.close(); sourceRef.current = null; }
    if (flushTimerRef.current) { clearTimeout(flushTimerRef.current); flushTimerRef.current = null; }
    flushFirstRef.current = 0;
    bufferRef.current = [];
    setLogs([]);
    setLoading(true);
    setError("");

    const url = serverPaths(projectId, nodeletId).containerLogs(containerId);
    const source = new EventSource(url);
    sourceRef.current = source;

    source.onopen = () => {
      setLoading(false);
      setError("");
    };
    source.onmessage = (event) => {
      try {
        bufferRef.current = [...bufferRef.current, JSON.parse(event.data) as LogEntry].slice(-MAX_LOGS);
        scheduleFlush();
      } catch { /* skip */ }
    };
    source.onerror = () => {
      setLoading(false);
      if (source.readyState === EventSource.CLOSED) {
        setError("日志流连接失败，请检查容器是否在运行");
      }
    };

    return () => {
      source.close();
      sourceRef.current = null;
    };
  }, [projectId, nodeletId, containerId, scheduleFlush]);

  // 组件卸载清理
  React.useEffect(() => {
    return () => {
      if (sourceRef.current) { sourceRef.current.close(); sourceRef.current = null; }
      if (flushTimerRef.current) clearTimeout(flushTimerRef.current);
    };
  }, []);

  const clear = React.useCallback(() => {
    if (flushTimerRef.current) { clearTimeout(flushTimerRef.current); flushTimerRef.current = null; }
    flushFirstRef.current = 0;
    bufferRef.current = [];
    setLogs([]);
  }, []);

  return { logs, loading, error, autoScroll, setAutoScroll, panelRef, clear };
}
