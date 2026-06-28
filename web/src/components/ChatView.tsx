import React from "react";
import { Sparkles, Send, Bot, User, Trash2 } from "lucide-react";
import type { ChatExchange, StepEvent } from "../types";
import { StepBlock } from "./StepBlock";
import { AnswerBlock } from "./AnswerBlock";
import { FormInput } from "./ui/FormInput";
import { Button } from "./ui/Button";

function withToolNames(steps: StepEvent[]): StepEvent[] {
  const names = new Map<string, string>();
  return steps.map((step) => {
    if (step.type === "tool_call" && step.toolCallId && step.toolName) {
      names.set(step.toolCallId, step.toolName);
      return step;
    }
    if (step.type === "tool_result" && !step.toolName && step.toolCallId) {
      const toolName = names.get(step.toolCallId);
      if (toolName) {
        return { ...step, toolName };
      }
    }
    return step;
  });
}

export function ChatView({
  chatExchanges,
  currentSteps,
  currentQuestion,
  chatInput,
  chatLoading,
  chatError,
  onInputChange,
  onSend,
  onClear,
}: {
  chatExchanges: ChatExchange[];
  currentSteps: StepEvent[];
  currentQuestion: string;
  chatInput: string;
  chatLoading: boolean;
  chatError: string;
  onInputChange: (value: string) => void;
  onSend: () => void;
  onClear: () => void;
}) {
  // 流式传输中是否已出现 error，用于停止工具调用的 running 动画。
  const streamError = React.useMemo(
    () => currentSteps.some((s) => s.type === "error"),
    [currentSteps],
  );

  // 预处理当前步骤，注入 toolName 并过滤掉 answer/error。
  const visibleCurrentSteps = React.useMemo(
    () => withToolNames(currentSteps.filter((s) => s.type !== "answer" && s.type !== "error")),
    [currentSteps],
  );

  // 预处理历史交换记录中的步骤（过滤并用 useMemo 缓存）。
  const processedExchanges = React.useMemo(
    () =>
      chatExchanges.map((ex) => ({
        ...ex,
        displaySteps: withToolNames(ex.steps.filter((s) => s.type !== "answer" && s.type !== "error")),
      })),
    [chatExchanges],
  );

  const hasContent = chatExchanges.length > 0 || currentSteps.length > 0;

  return (
    <section className="chat-panel" id="chat-section">
      <div className="chat-header">
        <span><Sparkles size={18} /> 智能助手</span>
        {hasContent && (
          <button onClick={onClear} title="清空对话" aria-label="清空对话">
            <Trash2 size={16} />
          </button>
        )}
      </div>
      <div className="chat-body" role="log" aria-live="polite">
        {chatExchanges.length === 0 && currentSteps.length === 0 && !chatLoading && !currentQuestion && !chatError && (
          <div className="chat-empty">问我任何关于当前环境的问题，例如"哪些容器在运行？"或"Redis 是否正常？"</div>
        )}
        {processedExchanges.map((ex, i) => (
          <div key={i} className="chat-exchange">
            <div className="chat-msg user">
              <div className="chat-avatar"><User size={16} /></div>
              <div className="chat-content">{ex.question}</div>
            </div>
            {ex.displaySteps.map((step, j) => (
              <StepBlock key={j} step={step} />
            ))}
            {ex.answer && <AnswerBlock content={ex.answer} animate={false} />}
            {ex.error && <div className="chat-error">{ex.error}</div>}
          </div>
        ))}
        {(currentSteps.length > 0 || chatLoading) && (
          <div className="chat-exchange">
            <div className="chat-msg user">
              <div className="chat-avatar"><User size={16} /></div>
              <div className="chat-content">{currentQuestion}</div>
            </div>
            {visibleCurrentSteps.map((step, j) => {
              // 若流中已出现 error，则所有工具调用视为已有结果，停止动画。
              const hasResult =
                streamError ||
                visibleCurrentSteps.slice(j + 1).some(
                  (s) =>
                    s.type === "tool_result" &&
                    (!step.toolCallId || s.toolCallId === step.toolCallId),
                );
              return <StepBlock key={j} step={step} animate hasResult={hasResult} />;
            })}
            {currentSteps.some((s) => s.type === "answer") && (
              <AnswerBlock
                content={currentSteps.find((s) => s.type === "answer")!.content}
                animate
              />
            )}
            {!currentSteps.some((s) => s.type === "answer") &&
              !streamError &&
              chatLoading && (
                <div className="chat-msg assistant">
                  <div className="chat-avatar"><Bot size={16} /></div>
                  <div className="chat-content chat-thinking">Thinking…</div>
                </div>
              )}
            {streamError && (
              <div className="chat-error">
                {currentSteps.find((s) => s.type === "error")!.content}
              </div>
            )}
          </div>
        )}
        {chatError && <div className="chat-error">{chatError}</div>}
      </div>
      <div className="chat-footer">
        <FormInput
          placeholder="输入问题，按 Enter 发送"
          value={chatInput}
          onChange={(e) => onInputChange(e.target.value)}
          onKeyDown={(e: React.KeyboardEvent) => {
            if (e.key === "Enter" && !e.nativeEvent.isComposing) {
              e.preventDefault();
              onSend();
            }
          }}
          disabled={chatLoading}
        />
        <Button
          onClick={() => onSend()}
          disabled={chatLoading || !chatInput.trim()}
          title="发送"
          aria-label="发送消息"
        >
          <Send size={18} />
        </Button>
      </div>
    </section>
  );
}
