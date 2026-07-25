import assert from "node:assert/strict";
import test from "node:test";

import {
  buildSessionTreeDisplay,
  isSessionTreeEntryVisible,
  sessionTreeEntryLabel,
  sessionTreeRowPrefix,
  type SessionTreeRow,
} from "../src/lib/sessionTree.ts";
import type { AgentMessage, SessionEntry } from "../src/types.ts";

const emptyUsage = {
  input: 0,
  output: 0,
  cacheRead: 0,
  cacheWrite: 0,
  totalTokens: 0,
};

test("tree only shows user and assistant messages", () => {
  const entries = [
    sessionInfo("s1"),
    userEntry("u1", "s1", "巡检服务器"),
    modelEntry("m1", "u1"),
    assistantEntry("a1", "m1", "我先检查容器"),
    toolResultEntry("t1", "a1", "call_1", "read", "result"),
    branchSummary("b1", "t1", "summary"),
  ];

  const tree = buildSessionTreeDisplay(entries, "a1");

  assert.deepEqual(rowIDs(tree.rows), ["u1", "a1"]);
  assert.equal(rowByID(tree.rows, "a1").visibleParentId, "u1");
  assert.equal(tree.activeEntryId, "a1");
  assert.equal(isSessionTreeEntryVisible(userEntry("u2", "", "hello")), true);
  assert.equal(isSessionTreeEntryVisible(assistantEntry("a2", "", "hi")), true);
  assert.equal(isSessionTreeEntryVisible(toolResultEntry("t2", "", "call_1", "read", "result")), false);
});

test("hidden tool result keeps assistant child under nearest visible message", () => {
  const entries = [
    userEntry("u1", "", "root"),
    assistantToolCallEntry("a1", "u1", "call_1", "read", { path: "README.md" }),
    toolResultEntry("t1", "a1", "call_1", "read", "file content"),
    assistantEntry("a2", "t1", "done"),
  ];

  const tree = buildSessionTreeDisplay(entries, "t1");

  assert.deepEqual(rowIDs(tree.rows), ["u1", "a1", "a2"]);
  assert.equal(rowByID(tree.rows, "a2").visibleParentId, "a1");
  assert.equal(tree.activeEntryId, "a1");
  assert.equal(rowByID(tree.rows, "u1").isActivePath, true);
  assert.equal(rowByID(tree.rows, "a1").isActivePath, true);
});

test("branch rows indent and active sibling is first", () => {
  const entries = [
    userEntry("u1", "", "root"),
    assistantEntry("a1", "u1", "answer 1"),
    assistantEntry("a2", "u1", "answer 2"),
  ];

  const tree = buildSessionTreeDisplay(entries, "a2");

  assert.deepEqual(rowIDs(tree.rows), ["u1", "a2", "a1"]);
  assert.deepEqual(pickRow(rowByID(tree.rows, "a2")), {
    id: "a2",
    visibleParentId: "u1",
    indent: 1,
    prefix: "├─ ",
    isLast: false,
    isVirtualRootChild: false,
    isActivePath: true,
  });
  assert.deepEqual(pickRow(rowByID(tree.rows, "a1")), {
    id: "a1",
    visibleParentId: "u1",
    indent: 1,
    prefix: "└─ ",
    isLast: true,
    isVirtualRootChild: false,
    isActivePath: false,
  });
});

test("tree appends realtime user and assistant rows from streaming messages", () => {
  const root = userEntry("u1", "", "old question");
  const entries = [root];
  const messages: AgentMessage[] = [
    root.message!,
    userMessage("new question", 2),
    assistantMessage("", 0),
  ];

  const tree = buildSessionTreeDisplay(entries, "u1", { messages });

  assert.deepEqual(rowIDs(tree.rows), ["u1", "runtime-1-user-2", "runtime-2-assistant-live"]);
  assert.equal(rowByID(tree.rows, "runtime-1-user-2").transient, true);
  assert.equal(rowByID(tree.rows, "runtime-2-assistant-live").label, "assistant: 回复中");
  assert.equal(tree.activeEntryId, "runtime-2-assistant-live");
});

test("realtime assistant row updates with generated text", () => {
  const root = userEntry("u1", "", "question");
  const entries = [root];
  const messages: AgentMessage[] = [
    root.message!,
    assistantMessage("正在分析", 0),
  ];

  const tree = buildSessionTreeDisplay(entries, "u1", { messages });

  assert.deepEqual(rowIDs(tree.rows), ["u1", "runtime-1-assistant-live"]);
  assert.equal(rowByID(tree.rows, "runtime-1-assistant-live").label, "assistant: 正在分析");
});

test("collapsed branch hides visible descendants", () => {
  const entries = [
    userEntry("u1", "", "root"),
    assistantEntry("a1", "u1", "answer 1"),
    userEntry("u2", "a1", "follow up"),
    assistantEntry("a2", "u1", "answer 2"),
  ];

  const tree = buildSessionTreeDisplay(entries, "a2", { collapsedIds: new Set(["a1"]) });

  assert.deepEqual(rowIDs(tree.rows), ["u1", "a2", "a1"]);
  assert.equal(rowByID(tree.rows, "a1").foldable, true);
  assert.equal(rowByID(tree.rows, "a1").collapsed, true);
});

test("collapsed active branch highlights nearest displayed ancestor", () => {
  const entries = [
    userEntry("u1", "", "root"),
    assistantEntry("a1", "u1", "answer 1"),
    userEntry("u2", "a1", "follow up"),
    assistantEntry("a2", "u2", "answer 2"),
    assistantEntry("a3", "u1", "answer 3"),
  ];

  const tree = buildSessionTreeDisplay(entries, "a2", { collapsedIds: new Set(["a1"]) });

  assert.deepEqual(rowIDs(tree.rows), ["u1", "a1", "a3"]);
  assert.equal(tree.activeEntryId, "a1");
});

test("multiple roots use virtual root rows and suppress root prefixes", () => {
  const entries = [
    userEntry("u1", "", "root 1"),
    userEntry("u2", "", "root 2"),
  ];

  const tree = buildSessionTreeDisplay(entries, "u2");

  assert.deepEqual(rowIDs(tree.rows), ["u2", "u1"]);
  assert.equal(rowByID(tree.rows, "u2").isVirtualRootChild, true);
  assert.equal(rowByID(tree.rows, "u2").showConnector, true);
  assert.equal(sessionTreeRowPrefix(rowByID(tree.rows, "u2")), "");
  assert.equal(rowByID(tree.rows, "u1").isVirtualRootChild, true);
  assert.equal(rowByID(tree.rows, "u1").showConnector, true);
  assert.equal(sessionTreeRowPrefix(rowByID(tree.rows, "u1")), "");
});

test("labels stay compact for text and tool-call-only assistant messages", () => {
  const text = "x".repeat(120);
  const long = userEntry("u1", "", text);
  const call = assistantToolCallEntry("a1", "u1", "call_1", "read", { path: "README.md" });

  assert.ok(sessionTreeEntryLabel(long).endsWith("…"));
  assert.equal(sessionTreeEntryLabel(call), "assistant: read");

  const content = long.message?.role === "user" ? long.message.content[0] : undefined;
  assert.equal(content?.type === "text" ? content.text : "", text);
});

function rowIDs(rows: SessionTreeRow[]): string[] {
  return rows.map((row) => row.id);
}

function rowByID(rows: SessionTreeRow[], id: string): SessionTreeRow {
  const row = rows.find((item) => item.id === id);
  assert.ok(row, `missing row ${id}`);
  return row;
}

function pickRow(row: SessionTreeRow) {
  return {
    id: row.id,
    visibleParentId: row.visibleParentId,
    indent: row.indent,
    prefix: sessionTreeRowPrefix(row),
    isLast: row.isLast,
    isVirtualRootChild: row.isVirtualRootChild,
    isActivePath: row.isActivePath,
  };
}

function sessionInfo(id: string): SessionEntry {
  return {
    type: "session_info",
    version: 1,
    id,
    timestamp: "2026-01-01T00:00:00Z",
    name: "session",
  };
}

function modelEntry(id: string, parentId: string): SessionEntry {
  return {
    type: "model_change",
    version: 1,
    id,
    parentId,
    timestamp: "2026-01-01T00:00:00Z",
    model: "model",
  };
}

function branchSummary(id: string, parentId: string, summary: string): SessionEntry {
  return {
    type: "branch_summary",
    version: 1,
    id,
    parentId,
    timestamp: "2026-01-01T00:00:00Z",
    summary,
  };
}

function userEntry(id: string, parentId: string, text: string): SessionEntry {
  return messageEntry(id, parentId, {
    role: "user",
    content: [{ type: "text", text }],
    timestamp: 1,
  });
}

function userMessage(text: string, timestamp: number): AgentMessage {
  return {
    role: "user",
    content: [{ type: "text", text }],
    timestamp,
  };
}

function assistantEntry(id: string, parentId: string, text: string): SessionEntry {
  return messageEntry(id, parentId, assistantMessage(text, 1));
}

function assistantMessage(text: string, timestamp: number): AgentMessage {
  return {
    role: "assistant",
    content: text ? [{ type: "text", text }] : [],
    usage: emptyUsage,
    stopReason: "stop",
    timestamp,
  };
}

function assistantToolCallEntry(
  id: string,
  parentId: string,
  callID: string,
  name: string,
  args: Record<string, unknown>,
): SessionEntry {
  return messageEntry(id, parentId, {
    role: "assistant",
    content: [{ type: "toolCall", id: callID, name, arguments: args }],
    usage: emptyUsage,
    stopReason: "toolUse",
    timestamp: 1,
  });
}

function toolResultEntry(id: string, parentId: string, toolCallId: string, toolName: string, text: string): SessionEntry {
  return messageEntry(id, parentId, {
    role: "toolResult",
    toolCallId,
    toolName,
    content: [{ type: "text", text }],
    isError: false,
    timestamp: 1,
  });
}

function messageEntry(id: string, parentId: string, message: AgentMessage): SessionEntry {
  return {
    type: "message",
    version: 1,
    id,
    parentId,
    timestamp: "2026-01-01T00:00:00Z",
    message,
  };
}
