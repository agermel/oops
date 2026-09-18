import assert from "node:assert/strict";
import test from "node:test";

import {
  applyAgentEventToSession,
  applyTerminalSnapshotToSession,
  EMPTY_AGENT_SESSION,
  sessionFromAPI,
  sessionInfosForTabs,
} from "../src/lib/session.ts";
import type { AgentEvent, AssistantMessage, SessionInfo, SessionResponse, ToolResultMessage } from "../src/types.ts";

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

const arrayFields = ["messages", "events", "tools", "entries"] as const;

function populatedSession(): SessionResponse {
  return {
    sessionId: "session_1",
    leafId: "leaf_1",
    messages: [{ role: "user", content: [{ type: "text", text: "keep this answer" }], timestamp: 1 }],
    events: [{ type: "agent_start" }],
    tools: [{ name: "read", description: "Read", parameters: { type: "object" } }],
    entries: [{ type: "message", version: 1, id: "leaf_1", timestamp: "2026-09-18T00:00:00Z" }],
  };
}

for (const field of arrayFields) {
  for (const value of [null, undefined, []]) {
    test(`keeps streamed ${field} when terminal value is ${JSON.stringify(value)}`, () => {
      const current = populatedSession();
      const terminal = { ...EMPTY_AGENT_SESSION, sessionId: "", [field]: value };
      const completed = applyTerminalSnapshotToSession(current, terminal);
      assert.deepEqual(completed, current);
      assert.equal(terminal[field], value);
    });
  }
}

test("normalizes missing and null arrays on detail load and terminal merge", () => {
  for (const detail of [
    { sessionId: "new_session" },
    { sessionId: "new_session", messages: null, events: null, tools: null, entries: null },
  ]) {
    const loaded = sessionFromAPI(detail);
    const merged = applyTerminalSnapshotToSession(populatedSession(), detail);
    for (const field of arrayFields) {
      assert.deepEqual(loaded[field], []);
      assert.deepEqual(merged[field], populatedSession()[field]);
    }
    assert.equal(loaded.sessionId, "new_session");
    assert.equal(merged.sessionId, "new_session");
  }
});

test("replaces populated terminal arrays and retains only empty fields", () => {
  const current = populatedSession();
  const terminal: SessionResponse = {
    ...populatedSession(),
    leafId: "leaf_2",
    editorText: "continue here",
    messages: [{ role: "user", content: [{ type: "text", text: "updated" }], timestamp: 2 }],
    events: [{ type: "turn_start", turn: 2 }],
    tools: [{ name: "write", description: "Write", parameters: { type: "object" } }],
    entries: [{ type: "message", version: 1, id: "leaf_2", timestamp: "2026-09-18T00:00:01Z" }],
  };
  assert.deepEqual(applyTerminalSnapshotToSession(current, terminal), terminal);
  assert.deepEqual(sessionFromAPI(terminal), terminal);
  const mixed = applyTerminalSnapshotToSession(current, { ...terminal, events: null, tools: undefined, entries: [] });
  assert.deepEqual(mixed, { ...terminal, events: current.events, tools: current.tools, entries: current.entries });
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
