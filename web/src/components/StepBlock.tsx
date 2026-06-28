import React from "react";
import { Bot, ChevronDown, ChevronRight } from "lucide-react";
import { StreamingText } from "./StreamingText";
import type { StepEvent } from "../types";

function summarizeToolArgs(toolArgs: string): string {
  try {
    const parsed: unknown = JSON.parse(toolArgs);
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      return Object.entries(parsed).map(([k, v]) => `${k}=${String(v)}`).join(", ");
    }
    return String(parsed ?? "");
  } catch {
    return toolArgs;
  }
}

export function StepBlock({ step, animate, hasResult }: { step: StepEvent; animate?: boolean; hasResult?: boolean }) {
  const [expanded, setExpanded] = React.useState(false);

  if (step.type === "thinking") {
    return (
      <div className="step-block step-thinking">
        <div className="step-icon"><Bot size={14} /></div>
        <div className="step-content">
          <StreamingText text={step.content} animate={animate ?? false} />
        </div>
      </div>
    );
  }

  if (step.type === "tool_call") {
    const running = animate && !hasResult;
    let argsSummary = "";
    if (step.toolArgs) {
      argsSummary = summarizeToolArgs(step.toolArgs);
    }
    return (
      <div className={`step-block step-tool-call ${running ? "step-running" : ""}`}>
        <div className="step-icon">🔧</div>
        <div className="step-content">
          {running ? (
            <span className="tool-shimmer">调用 {step.toolName || step.content}...</span>
          ) : (
            <>
              <span className="step-tool-name">调用 {step.toolName || step.content}</span>
              {argsSummary && <span className="step-tool-args">（{argsSummary}）</span>}
            </>
          )}
        </div>
      </div>
    );
  }

  if (step.type === "tool_result") {
    let formatted = step.content;
    const resultName = step.toolName || step.toolCallId || "工具";
    try {
      formatted = JSON.stringify(JSON.parse(step.content), null, "  ");
    } catch {
      // 不是 JSON，保持原样。
    }
    return (
      <div className="step-block step-tool-result">
        <div className="step-icon">{expanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}</div>
        <div className="step-content">
          <button className="step-toggle" onClick={() => setExpanded((v) => !v)}>
            {expanded ? "收起" : "展开"} {resultName} 返回
          </button>
          <div className={`step-result-wrap ${expanded ? "open" : ""}`}>
            <pre className="step-result">{formatted}</pre>
          </div>
        </div>
      </div>
    );
  }

  return null;
}
