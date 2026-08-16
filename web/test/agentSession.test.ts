import assert from "node:assert/strict";
import test from "node:test";

import {
  applyAgentEventToSession,
  applyTerminalSnapshotToSession,
  EMPTY_AGENT_SESSION,
  sessionInfosForTabs,
} from "../src/lib/session.ts";
import type { AgentEvent, AssistantMessage, SessionInfo, ToolResultMessage } from "../src/types.ts";

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

test("keeps streamed history when the terminal snapshot is bounded", () => {
  const message: AssistantMessage = {
    role: "assistant",
    content: [{ type: "text", text: "answer" }],
    usage: emptyUsage,
    stopReason: "stop",
    timestamp: 1,
  };
  const event: AgentEvent = { type: "message_end", message };
  const current = {
    ...EMPTY_AGENT_SESSION,
    sessionId: "session_1",
    leafId: "leaf_1",
    messages: [message],
    events: [event],
  };
  const terminal = {
    ...EMPTY_AGENT_SESSION,
    sessionId: "session_1",
    leafId: "leaf_2",
  };

  const completed = applyTerminalSnapshotToSession(current, terminal);

  assert.equal(completed.sessionId, "session_1");
  assert.equal(completed.leafId, "leaf_2");
  assert.deepEqual(completed.messages, [message]);
  assert.deepEqual(completed.events, [event]);
});

test("keeps an existing active session in the server-provided order", () => {
  const sessions: SessionInfo[] = [
    sessionInfo("first", 1),
    sessionInfo("second", 2),
    sessionInfo("third", 3),
  ];

  const ordered = sessionInfosForTabs(sessions, "third");

  assert.deepEqual(ordered.map((session) => session.id), ["first", "second", "third"]);
});

test("prepends a missing active session fallback", () => {
  const sessions: SessionInfo[] = [sessionInfo("history", 1)];
  const active: SessionInfo = sessionInfo("active", 1);

  const ordered = sessionInfosForTabs(sessions, active.id, active);

  assert.deepEqual(ordered.map((session) => session.id), ["active", "history"]);
});

function sessionInfo(id: string, questionCount: number): SessionInfo {
  return {
    id,
    questionCount,
    createdAt: 1,
    updatedAt: 1,
  };
}
