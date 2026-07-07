import React from "react";
import { Sparkles, Send, Bot, User, Trash2, Plus, MessageSquare, ChevronDown, ChevronUp } from "lucide-react";
import * as Icons from "lucide-react";
import type { LucideIcon } from "lucide-react";
import type { ChatExchange, StepEvent, SessionInfo } from "../types";
import { CHAT_MAX_TOKENS, CHAT_MAX_SESSION_BADGES } from "../types";
import { StepBlock } from "./StepBlock";
import { AnswerBlock } from "./AnswerBlock";
import { FormInput } from "./ui/FormInput";
import { Button } from "./ui/Button";

// 工具事件类型（展示用），不含 answer/error/session/stats 等终端事件。
const displayEventTypes = new Set(["thinking", "tool_call", "tool_result"]);

// 已知图标名 → lucide 组件映射。
const knownIcons: Record<string, LucideIcon> = {
  Stethoscope: Icons.Stethoscope,
  ClipboardCheck: Icons.ClipboardCheck,
  Sparkles: Icons.Sparkles,
  Wrench: Icons.Wrench,
  Cog: Icons.Cog,
  Search: Icons.Search,
  BookOpen: Icons.BookOpen,
  Zap: Icons.Zap,
  Bug: Icons.Bug,
  Shield: Icons.Shield,
  Eye: Icons.Eye,
};

function getIcon(name: string): LucideIcon {
  return knownIcons[name] || Icons.Sparkles;
}

type SkillMeta = { label: string; iconName: string; color: string };

// 从当前步骤中检测最近的 skill 工具调用，返回技能名称。
function detectActiveSkill(steps: StepEvent[]): string | null {
  for (let i = steps.length - 1; i >= 0; i--) {
    if (steps[i].type === "tool_call" && steps[i].toolName === "skill" && steps[i].toolArgs) {
      try {
        const args = JSON.parse(steps[i].toolArgs!);
        if (args.name) return args.name;
      } catch { /* ignore */ }
    }
  }
  return null;
}

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

// countAgentSteps 统计当前步骤中的工具调用数。
function countAgentSteps(steps: StepEvent[]): number {
  let count = 0;
  for (const s of steps) {
    if (s.type === "tool_call") count++;
  }
  return count;
}

export function ChatView({
  chatExchanges,
  currentSteps,
  currentQuestion,
  chatInput,
  chatLoading,
  chatError,
  sessionId,
  sessions,
  agentType,
  maxStep,
  tokenStats,
  agentMeta,
  onInputChange,
  onSend,
  onClear,
  onNewChat,
  onSelectSession,
}: {
  chatExchanges: ChatExchange[];
  currentSteps: StepEvent[];
  currentQuestion: string;
  chatInput: string;
  chatLoading: boolean;
  chatError: string;
  sessionId: string;
  sessions: SessionInfo[];
  agentType: string;
  maxStep: number;
  tokenStats: { tokens: number; trimmed: number } | null;
  agentMeta: Record<string, SkillMeta>;
  onInputChange: (value: string) => void;
  onSend: () => void;
  onClear: () => void;
  onNewChat: () => void;
  onSelectSession: (id: string) => void;
}) {
  // 流式传输中是否已出现 error，用于停止工具调用的 running 动画。
  const streamError = React.useMemo(
    () => currentSteps.some((s) => s.type === "error"),
    [currentSteps],
  );

  // 预处理当前步骤，注入 toolName 并过滤掉 answer/error/session/stats。
  const visibleCurrentSteps = React.useMemo(
    () => withToolNames(currentSteps.filter((s) => displayEventTypes.has(s.type))),
    [currentSteps],
  );

  // 预处理历史交换记录中的步骤。
  const processedExchanges = React.useMemo(
    () =>
      chatExchanges.map((ex) => ({
        ...ex,
        displaySteps: withToolNames(ex.steps.filter((s) => displayEventTypes.has(s.type))),
      })),
    [chatExchanges],
  );

  const [expandAll, setExpandAll] = React.useState(false);
  const hasContent = chatExchanges.length > 0 || currentSteps.length > 0;
  const otherSessions = sessions.filter((s) => s.id !== sessionId);
  const stepCount = countAgentSteps(currentSteps);
  // 优先从当前步骤中检测 skill 工具调用，回退到 session 中的 agentType。
  const activeSkill = detectActiveSkill(currentSteps);
  const displayType = activeSkill || agentType;
  const meta: { label: string; icon: React.ReactNode; cls: string } | null = (() => {
    const sm = displayType ? agentMeta[displayType] : null;
    if (sm) {
      const IconComponent = getIcon(sm.iconName);
      return { label: sm.label, icon: <IconComponent size={14} />, cls: `agent-badge-${sm.color}` };
    }
    // 回退到旧硬编码列表。
    if (displayType === "diagnose") return { label: "诊断", icon: <Icons.Stethoscope size={14} />, cls: "agent-badge-diagnose" };
    if (displayType === "inspect") return { label: "巡检", icon: <Icons.ClipboardCheck size={14} />, cls: "agent-badge-inspect" };
    if (displayType) return { label: displayType, icon: <Sparkles size={14} />, cls: "agent-badge-custom" };
    return null;
  })();

  return (
    <section className="chat-panel" id="chat-section">
      <div className="chat-header">
        <div className="chat-header-left">
          <Sparkles size={18} />
          <span>智能助手</span>
          {meta && chatLoading && (
            <span className={`agent-badge ${meta.cls}`}>
              {meta.icon}
              <span>{meta.label} Agent</span>
            </span>
          )}
          {meta && maxStep > 0 && chatLoading && (
            <span className="agent-step-counter">
              Step {stepCount}/{maxStep}
            </span>
          )}
        </div>
        <span className="chat-header-actions">
          {hasContent && (
            <>
              <button
                onClick={() => setExpandAll((v) => !v)}
                title={expandAll ? "折叠全部步骤" : "展开全部步骤"}
                aria-label={expandAll ? "折叠全部步骤" : "展开全部步骤"}
              >
                {expandAll ? <ChevronUp size={16} /> : <ChevronDown size={16} />}
              </button>
              <button onClick={onNewChat} title="新建对话" aria-label="新建对话">
                <Plus size={16} />
              </button>
              <button onClick={onClear} title="清空当前对话" aria-label="清空当前对话">
                <Trash2 size={16} />
              </button>
            </>
          )}
        </span>
      </div>
      {otherSessions.length > 0 && (
        <div className="chat-sessions-bar">
          <MessageSquare size={14} />
          <span className="chat-sessions-label">历史会话</span>
          {otherSessions.slice(0, CHAT_MAX_SESSION_BADGES).map((s) => (
            <button
              key={s.id}
              className="chat-session-badge"
              title={`${s.messageCount} 条消息 — 点击切换`}
              onClick={() => onSelectSession(s.id)}
            >
              {(s.id || "").slice(-8)}
            </button>
          ))}
        </div>
      )}
      <div className="chat-body" role="log" aria-live="polite">
        {chatExchanges.length === 0 && currentSteps.length === 0 && !chatLoading && !currentQuestion && (
          <div className="chat-empty">问我任何关于当前环境的问题，例如"哪些容器在运行？"或"Redis 是否正常？"</div>
        )}
        {processedExchanges.map((ex, i) => (
          <div key={`${i}-${ex.question.length}-${ex.question.slice(0, 20)}`} className="chat-exchange">
            <div className="chat-msg user">
              <div className="chat-avatar"><User size={16} /></div>
              <div className="chat-content">{ex.question}</div>
            </div>
            {ex.displaySteps.map((step, j) => (
              <StepBlock key={step.toolCallId || `${step.type}-${j}`} step={step} forceExpand={expandAll} />
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
              const hasResult =
                step.type === "error" ||
                streamError ||
                visibleCurrentSteps.slice(j + 1).some(
                  (s) =>
                    s.type === "tool_result" &&
                    (!step.toolCallId || s.toolCallId === step.toolCallId),
                );
              return <StepBlock key={step.toolCallId || `${step.type}-${j}`} step={step} animate hasResult={hasResult} forceExpand={expandAll} />;
            })}
            {currentSteps.some((s) => s.type === "answer") && (
              <AnswerBlock
                content={currentSteps.filter((s) => s.type === "answer").map((s) => s.content).join("")}
                animate
              />
            )}
            {!currentSteps.some((s) => s.type === "answer") &&
              !streamError &&
              !currentSteps.some((s) => s.type === "thinking") &&
              chatLoading && (
                <div className="chat-msg assistant">
                  <div className="chat-avatar"><Bot size={16} /></div>
                  <div className="chat-content chat-thinking">
                    {currentQuestion ? "Thinking…" : "加载中…"}
                  </div>
                </div>
              )}
            {streamError && (
              <div className="chat-error">
                {currentSteps.find((s) => s.type === "error")?.content || "未知错误"}
              </div>
            )}
          </div>
        )}
        {chatError && <div className="chat-error">{chatError}</div>}
      </div>
      <div className="chat-footer">
        <div className="chat-footer-main">
          <FormInput
            multiline
            className="chat-input"
            placeholder="输入问题，Enter 发送，Shift+Enter 换行"
            value={chatInput}
            onChange={(e) => onInputChange(e.target.value)}
            onKeyDown={(e: React.KeyboardEvent) => {
              if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
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
        {tokenStats && (
          <div className={`chat-token-stats ${tokenStats.trimmed > 0 ? "token-trimmed" : ""}`}>
            <span>Token: ~{tokenStats.tokens.toLocaleString()} / {CHAT_MAX_TOKENS.toLocaleString()}</span>
            {tokenStats.trimmed > 0 && (
              <span className="token-trimmed-hint">（已裁剪 {tokenStats.trimmed} 条消息）</span>
            )}
          </div>
        )}
      </div>
    </section>
  );
}
