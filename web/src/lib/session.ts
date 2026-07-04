import type { SessionMessage, ChatExchange, StepEvent } from "../types";

/**
 * Converts a flat SessionMessage[] into a structured ChatExchange[].
 * Used both during initial session load and session switching.
 */
export function deserializeSessionMessages(messages: SessionMessage[]): ChatExchange[] {
  const exchanges: ChatExchange[] = [];
  let currentQuestion = "";
  let currentSteps: StepEvent[] = [];

  for (const msg of messages) {
    if (msg.role === "user") {
      if (currentQuestion) {
        exchanges.push({ question: currentQuestion, steps: currentSteps });
      }
      currentQuestion = msg.content;
      currentSteps = [];
    } else if (msg.role === "assistant") {
      currentSteps.push({ type: "answer", content: msg.content });
    } else if (msg.role === "thinking") {
      currentSteps.push({ type: "thinking", content: msg.content });
    } else if (msg.role === "tool_call") {
      currentSteps.push({
        type: "tool_call",
        content: msg.content,
        toolCallId: msg.toolCallId,
        toolName: msg.toolName,
        toolArgs: msg.toolArgs,
      });
    } else if (msg.role === "tool") {
      currentSteps.push({
        type: "tool_result",
        content: msg.content,
        toolCallId: msg.toolCallId,
        toolName: msg.toolName,
      });
    }
  }

  if (currentQuestion) {
    const answerContents = currentSteps
      .filter((s) => s.type === "answer")
      .map((s) => s.content);
    exchanges.push({
      question: currentQuestion,
      steps: currentSteps,
      answer: answerContents.length > 0 ? answerContents.join("") : undefined,
    });
  }

  return exchanges;
}
