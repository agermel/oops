import React from "react";
import { Bot, ChevronDown, ChevronRight } from "lucide-react";
import { Streamdown } from "streamdown";
import { useTypewriter } from "../hooks/useTypewriter";
import type { StepEvent } from "../types";

function StreamingText({ text, animate }: { text: string; animate: boolean }) {
  const displayed = useTypewriter(text, animate);
  const done = !animate || displayed.length >= text.length;
  return (
    <>
      <Streamdown mode={animate ? "streaming" : "static"} controls={false}>{displayed}</Streamdown>
      {!done && <span className="cursor-blink">|</span>}
    </>
  );
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
      try {
        const parsed = JSON.parse(step.toolArgs);
        argsSummary = Object.entries(parsed).map(([k, v]) => `${k}=${v}`).join(", ");
      } catch {
        argsSummary = step.toolArgs;
      }
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
            {expanded ? "收起" : "展开"}工具返回
          </button>
          {expanded && <pre className="step-result">{formatted}</pre>}
        </div>
      </div>
    );
  }

  return null;
}
