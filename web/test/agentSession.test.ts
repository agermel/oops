import assert from "node:assert/strict";
import test from "node:test";

import { applyAgentEventToSession, EMPTY_AGENT_SESSION } from "../src/lib/session.ts";
import type { AgentEvent, AssistantMessage, ToolResultMessage } from "../src/types.ts";

const emptyUsage = {
  input: 0,
  output: 0,
  cacheRead: 0,
  cacheWrite: 0,
  totalTokens: 0,
};

test("applies streamed assistant message updates in place", () => {
  const first: AssistantMessage = {
    role: "assistant",
    content: [{ type: "text", text: "hel" }],
    usage: emptyUsage,
    stopReason: "stop",
    timestamp: 1,
  };
  const second: AssistantMessage = {
    ...first,
    content: [{ type: "text", text: "hello" }],
  };
  const startEvent: AgentEvent = { type: "message_start", message: first };
  const updateEvent: AgentEvent = {
    type: "message_update",
    message: second,
    assistantMessageEvent: {
      type: "text_delta",
      contentIndex: 0,
      delta: "lo",
      partial: second,
    },
  };

  const started = applyAgentEventToSession(EMPTY_AGENT_SESSION, startEvent);
  const updated = applyAgentEventToSession(started, updateEvent);

  assert.equal(updated.events.length, 2);
  assert.equal(updated.messages.length, 1);
  assert.deepEqual(updated.messages[0], second);
});

test("upserts tool results by tool call id", () => {
  const first: ToolResultMessage = {
    role: "toolResult",
    toolCallId: "call_1",
    toolName: "read",
    content: [{ type: "text", text: "old" }],
    isError: false,
    timestamp: 1,
  };
  const second: ToolResultMessage = {
    ...first,
    content: [{ type: "text", text: "new" }],
    timestamp: 2,
  };

  const started = applyAgentEventToSession(EMPTY_AGENT_SESSION, { type: "message_start", message: first });
  const ended = applyAgentEventToSession(started, { type: "message_end", message: second });

  assert.equal(ended.messages.length, 1);
  assert.deepEqual(ended.messages[0], second);
});
