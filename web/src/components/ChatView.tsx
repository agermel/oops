import { Sparkles, Send, Bot, User } from "lucide-react";
import type { ChatExchange, StepEvent } from "../types";
import { StepBlock } from "./StepBlock";
import { AnswerBlock } from "./AnswerBlock";

export function ChatView({
  chatExchanges,
  currentSteps,
  currentQuestion,
  chatInput,
  chatLoading,
  chatError,
  onInputChange,
  onSend,
}: {
  chatExchanges: ChatExchange[];
  currentSteps: StepEvent[];
  currentQuestion: string;
  chatInput: string;
  chatLoading: boolean;
  chatError: string;
  onInputChange: (value: string) => void;
  onSend: () => void;
}) {
  return (
    <section className="chat-panel" id="chat-section">
      <div className="chat-header">
        <span><Sparkles size={18} /> 智能助手</span>
      </div>
      <div className="chat-body">
        {chatExchanges.length === 0 && currentSteps.length === 0 && (
          <div className="chat-empty">问我任何关于当前环境的问题，例如"哪些容器在运行？"或"Redis 是否正常？"</div>
        )}
        {chatExchanges.map((ex, i) => (
          <div key={i} className="chat-exchange">
            <div className="chat-msg user">
              <div className="chat-avatar"><User size={16} /></div>
              <div className="chat-content">{ex.question}</div>
            </div>
            {ex.steps.filter((s) => s.type !== "answer" && s.type !== "error").map((step, j) => (
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
            {currentSteps.filter((s) => s.type !== "answer" && s.type !== "error").map((step, j) => {
              const hasResult = currentSteps.slice(j + 1).some(s => s.type === "tool_result");
              return <StepBlock key={j} step={step} animate hasResult={hasResult} />;
            })}
            {currentSteps.find((s) => s.type === "answer") && (
              <AnswerBlock content={currentSteps.find((s) => s.type === "answer")!.content} animate />
            )}
            {!currentSteps.find((s) => s.type === "answer") && !currentSteps.find((s) => s.type === "error") && chatLoading && (
              <div className="chat-msg assistant">
                <div className="chat-avatar"><Bot size={16} /></div>
                <div className="chat-content chat-thinking">Thinking…</div>
              </div>
            )}
            {currentSteps.find((s) => s.type === "error") && (
              <div className="chat-error">{currentSteps.find((s) => s.type === "error")!.content}</div>
            )}
          </div>
        )}
        {chatError && <div className="chat-error">{chatError}</div>}
      </div>
      <div className="chat-footer">
        <input
          placeholder="输入问题，按 Enter 发送"
          value={chatInput}
          onChange={(e) => onInputChange(e.target.value)}
          onKeyDown={(e) => { if (e.key === "Enter") { onSend(); } }}
          disabled={chatLoading}
        />
        <button onClick={() => onSend()} disabled={chatLoading || !chatInput.trim()} title="发送">
          <Send size={18} />
        </button>
      </div>
    </section>
  );
}
