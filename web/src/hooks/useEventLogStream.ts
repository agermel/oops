import React from "react";
import type { LogEntry } from "../types";
import { MAX_LOGS, LOG_FLUSH_MS, LOG_MAX_WAIT_MS } from "../types";

type EventLogStreamOptions = {
  activeKey: string;
  url: string;
  errorMessage: string;
};

export function useEventLogStream({ activeKey, url, errorMessage }: EventLogStreamOptions) {
  const [logs, setLogs] = React.useState<LogEntry[]>([]);
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState("");
  const [autoScroll, setAutoScroll] = React.useState(true);
  const panelRef = React.useRef<HTMLDivElement | null>(null);

  const sourceRef = React.useRef<EventSource | null>(null);
  const bufferRef = React.useRef<LogEntry[]>([]);
  const flushTimerRef = React.useRef<ReturnType<typeof setTimeout> | null>(null);
  const flushFirstRef = React.useRef<number>(0);

  const resetFlushTimer = React.useCallback(() => {
    if (flushTimerRef.current) {
      clearTimeout(flushTimerRef.current);
      flushTimerRef.current = null;
    }
    flushFirstRef.current = 0;
  }, []);

  const resetBuffer = React.useCallback(() => {
    resetFlushTimer();
    bufferRef.current = [];
  }, [resetFlushTimer]);

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
      resetFlushTimer();
      doFlushRef.current();
      return;
    }
    if (flushTimerRef.current) clearTimeout(flushTimerRef.current);
    flushTimerRef.current = setTimeout(() => {
      flushTimerRef.current = null;
      flushFirstRef.current = 0;
      doFlushRef.current();
    }, LOG_FLUSH_MS);
  }, [resetFlushTimer]);

  const activeRef = React.useRef("");
  React.useEffect(() => {
    if (!activeKey || !url) {
      if (sourceRef.current) {
        sourceRef.current.close();
        sourceRef.current = null;
      }
      resetBuffer();
      activeRef.current = "";
      setLogs([]);
      return;
    }
    if (activeRef.current === activeKey) return;
    activeRef.current = activeKey;

    if (sourceRef.current) {
      sourceRef.current.close();
      sourceRef.current = null;
    }
    resetBuffer();
    setLogs([]);
    setLoading(true);
    setError("");

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
      } catch {
        // skip
      }
    };
    source.onerror = () => {
      setLoading(false);
      if (source.readyState === EventSource.CLOSED) {
        setError(errorMessage);
      }
    };

    return () => {
      source.close();
      sourceRef.current = null;
    };
  }, [activeKey, url, errorMessage, resetBuffer, scheduleFlush]);

  React.useEffect(() => {
    return () => {
      if (sourceRef.current) {
        sourceRef.current.close();
        sourceRef.current = null;
      }
      resetBuffer();
    };
  }, [resetBuffer]);

  const clear = React.useCallback(() => {
    resetBuffer();
    setLogs([]);
  }, [resetBuffer]);

  return { logs, loading, error, autoScroll, setAutoScroll, panelRef, clear };
}
