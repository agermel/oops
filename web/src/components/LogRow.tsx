import type { LogEntry } from "../types";
import { ansiConvertor } from "../types";

export function LogRow({ entry }: { entry: LogEntry }) {
  const level = entry.level || "unknown";
  const html = ansiConvertor.toHtml(entry.rawMessage || entry.message || "");

  return (
    <div className={`log-line level-${level}`}>
      <span>{entry.timestamp || "-"}</span>
      <b className={`log-stream-tag ${entry.stream === "stderr" ? "stderr" : "stdout"}`}>{entry.stream}</b>
      <strong className={`log-level ${level}`}>{level}</strong>
      <code dangerouslySetInnerHTML={{ __html: html }} />
    </div>
  );
}
