import type { LogEntry } from "../types";
import { ansiConvertor } from "../types";

// sanitizeHTML 移除危险的 HTML 标签和属性，仅保留 ANSI 转换器生成的 span。
function sanitizeHTML(html: string): string {
  // 移除 script/iframe/object/embed 标签及其内容。
  return html
    .replace(/<script[\s\S]*?<\/script>/gi, "")
    .replace(/<iframe[\s\S]*?<\/iframe>/gi, "")
    .replace(/<object[\s\S]*?<\/object>/gi, "")
    .replace(/<embed[\s\S]*?>/gi, "")
    .replace(/\bon\w+\s*=\s*"[^"]*"/gi, "")
    .replace(/\bon\w+\s*=\s*'[^']*'/gi, "")
    .replace(/javascript\s*:/gi, "");
}

export function LogRow({ entry }: { entry: LogEntry }) {
  const level = entry.level || "unknown";
  // ansiConvertor 已配置 escapeXML: true 对 XML 实体做转义；
  // sanitizeHTML 提供额外一层防御，剥离 JS 事件属性和危险标签。
  const raw = ansiConvertor.toHtml(entry.rawMessage || entry.message || "");
  const html = sanitizeHTML(raw);

  return (
    <div className={`log-line level-${level}`}>
      <span>{entry.timestamp || "-"}</span>
      <b className={`log-stream-tag ${entry.stream === "stderr" ? "stderr" : "stdout"}`}>{entry.stream}</b>
      <strong className={`log-level ${level}`}>{level}</strong>
      <code dangerouslySetInnerHTML={{ __html: html }} />
    </div>
  );
}
