import type { LogEntry } from "../types";
import { ansiConvertor } from "../types";
import { sanitizeHTML } from "../lib/sanitize";

export function LogRow({ entry }: { entry: LogEntry }) {
  const level = entry.level || "unknown";
  const stream = entry.stream || "system";
  const streamClass = stream === "stderr" || stream === "sse" || stream === "system" || stream === "stdout"
    ? stream
    : "other";
  const streamLabel = stream === "system" ? "系统" : stream;
  const raw = ansiConvertor.toHtml(entry.rawMessage || entry.message || "");
  const html = sanitizeHTML(raw);

  return (
    <div className={`log-line level-${level}`}>
      <span>{entry.timestamp || "-"}</span>
      <b className={`log-stream-tag ${streamClass}`}>{streamLabel}</b>
      <strong className={`log-level ${level}`}>{level}</strong>
      <code dangerouslySetInnerHTML={{ __html: html }} />
    </div>
  );
}
