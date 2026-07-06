import React from "react";
import type { LogEntry } from "../types";
import { MAX_LOGS, LOG_FLUSH_MS, LOG_MAX_WAIT_MS } from "../types";
import { mcpConnectionPaths } from "../lib/paths";

export function useMCPLogStream(connectionId: string) {
  const [logs, setLogs] = React.useState<LogEntry[]>([]);
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState("");
  const [autoScroll, setAutoScroll] = React.useState(true);
  const panelRef = React.useRef<HTMLDivElement | null>(null);

  const sourceRef = React.useRef<EventSource | null>(null);
  const bufferRef = React.useRef<LogEntry[]>([]);
  const flushTimerRef = React.useRef<ReturnType<typeof setTimeout> | null>(null);
  const flushFirstRef = React.useRef<number>(0);

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

  const activeRef = React.useRef("");
  React.useEffect(() => {
    if (!connectionId) {
      if (sourceRef.current) { sourceRef.current.close(); sourceRef.current = null; }
      if (flushTimerRef.current) { clearTimeout(flushTimerRef.current); flushTimerRef.current = null; }
      flushFirstRef.current = 0;
      bufferRef.current = [];
      activeRef.current = "";
      setLogs([]);
      return;
    }
    if (activeRef.current === connectionId) return;
    activeRef.current = connectionId;

    if (sourceRef.current) { sourceRef.current.close(); sourceRef.current = null; }
    if (flushTimerRef.current) { clearTimeout(flushTimerRef.current); flushTimerRef.current = null; }
    flushFirstRef.current = 0;
    bufferRef.current = [];
    setLogs([]);
    setLoading(true);
    setError("");

    const source = new EventSource(mcpConnectionPaths.logsStream(connectionId));
    sourceRef.current = source;

    source.onopen = () => {
      setLoading(false);
      setError("");
    };
    source.onmessage = (event) => {
      try {
        bufferRef.current = [...bufferRef.current, JSON.parse(event.data) as LogEntry].slice(-MAX_LOGS);
        scheduleFlush();
      } catch {
        // skip
      }
    };
    source.onerror = () => {
      setLoading(false);
      if (source.readyState === EventSource.CLOSED) {
        setError("MCP 日志流连接失败，请检查连接状态");
      }
    };

    return () => {
      source.close();
      sourceRef.current = null;
    };
  }, [connectionId, scheduleFlush]);

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
