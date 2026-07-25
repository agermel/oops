import type { AgentMessage, SessionEntry } from "../types";

export type SessionTreeGutter = {
  position: number;
  show: boolean;
};

export type SessionTreeRow = {
  entry: SessionEntry;
  id: string;
  label: string;
  sourceIndex: number;
  originalParentId: string;
  visibleParentId: string | null;
  indent: number;
  displayIndent: number;
  showConnector: boolean;
  isLast: boolean;
  isVirtualRootChild: boolean;
  gutters: SessionTreeGutter[];
  hasVisibleChildren: boolean;
  isActivePath: boolean;
  foldable: boolean;
  collapsed: boolean;
  transient: boolean;
};

export type SessionTreeDisplay = {
  rows: SessionTreeRow[];
  activeEntryId: string;
  visibleCount: number;
};

export type SessionTreeOptions = {
  messages?: AgentMessage[];
  collapsedIds?: ReadonlySet<string>;
};

type TreeEntry = SessionEntry & {
  transient?: boolean;
};

type RawNode = {
  entry: TreeEntry;
  id: string;
  label: string;
  sourceIndex: number;
  parentId: string;
  children: RawNode[];
  containsActive: boolean;
};

type StackItem = {
  id: string;
  indent: number;
  showConnector: boolean;
  isLast: boolean;
  gutters: SessionTreeGutter[];
  isVirtualRootChild: boolean;
};

const TREE_LABEL_MAX_CHARS = 72;

export function buildSessionTreeDisplay(
  entries: SessionEntry[],
  activeLeafId = "",
  options: SessionTreeOptions = {},
): SessionTreeDisplay {
  const { entries: treeEntries, activeLeafId: treeActiveLeafId } = mergeRealtimeEntries(
    entries,
    options.messages || [],
    activeLeafId,
  );
  const collapsedIds = options.collapsedIds || new Set<string>();
  const raw = buildRawTree(treeEntries, treeActiveLeafId);
  const visibleIDs = new Set<string>();
  const visibleNodes: RawNode[] = [];

  for (const node of raw.nodes) {
    if (isSessionTreeEntryVisible(node.entry)) {
      visibleIDs.add(node.id);
      visibleNodes.push(node);
    }
  }

  const visibleParentMap = new Map<string, string | null>();
  const visibleChildrenMap = new Map<string | null, string[]>();
  visibleChildrenMap.set(null, []);

  for (const node of visibleNodes) {
    const visibleParentId = nearestVisibleAncestorID(node.parentId, raw.byID, visibleIDs);
    visibleParentMap.set(node.id, visibleParentId);
    if (!visibleChildrenMap.has(visibleParentId)) {
      visibleChildrenMap.set(visibleParentId, []);
    }
    visibleChildrenMap.get(visibleParentId)?.push(node.id);
  }

  for (const [parentId, children] of visibleChildrenMap) {
    visibleChildrenMap.set(parentId, orderVisibleSiblings(children, raw.byID));
  }

  const multipleRoots = (visibleChildrenMap.get(null) || []).length > 1;
  const rows: SessionTreeRow[] = [];
  const stack: StackItem[] = [];
  const rootIDs = visibleChildrenMap.get(null) || [];

  for (let index = rootIDs.length - 1; index >= 0; index--) {
    stack.push({
      id: rootIDs[index],
      indent: multipleRoots ? 1 : 0,
      showConnector: multipleRoots,
      isLast: index === rootIDs.length - 1,
      gutters: [],
      isVirtualRootChild: multipleRoots,
    });
  }

  while (stack.length > 0) {
    const item = stack.pop();
    if (!item) continue;
    const node = raw.byID.get(item.id);
    if (!node) continue;

    const visibleChildren = visibleChildrenMap.get(item.id) || [];
    const displayIndent = multipleRoots ? Math.max(0, item.indent - 1) : item.indent;
    const foldable = isFoldable(node.id, visibleParentMap, visibleChildrenMap);
    const collapsed = foldable && collapsedIds.has(node.id);
    rows.push({
      entry: node.entry,
      id: node.id,
      label: node.label,
      sourceIndex: node.sourceIndex,
      originalParentId: node.parentId,
      visibleParentId: visibleParentMap.get(node.id) ?? null,
      indent: item.indent,
      displayIndent,
      showConnector: item.showConnector,
      isLast: item.isLast,
      isVirtualRootChild: item.isVirtualRootChild,
      gutters: item.gutters,
      hasVisibleChildren: visibleChildren.length > 0,
      isActivePath: raw.activePathIDs.has(node.id),
      foldable,
      collapsed,
      transient: node.entry.transient === true,
    });

    if (collapsed) {
      continue;
    }

    const multipleChildren = visibleChildren.length > 1;
    const childIndent = multipleChildren ? item.indent + 1 : item.indent;
    const connectorDisplayed = item.showConnector && !item.isVirtualRootChild;
    const connectorPosition = Math.max(0, displayIndent - 1);
    const childGutters = connectorDisplayed
      ? [...item.gutters, { position: connectorPosition, show: !item.isLast }]
      : item.gutters;

    for (let index = visibleChildren.length - 1; index >= 0; index--) {
      stack.push({
        id: visibleChildren[index],
        indent: childIndent,
        showConnector: multipleChildren,
        isLast: index === visibleChildren.length - 1,
        gutters: childGutters,
        isVirtualRootChild: false,
      });
    }
  }

  const displayedIDs = new Set(rows.map((row) => row.id));
  return {
    rows,
    activeEntryId: nearestVisibleEntryID(treeActiveLeafId, raw.byEntry, displayedIDs),
    visibleCount: rows.length,
  };
}

export function isSessionTreeEntryVisible(entry: SessionEntry): boolean {
  return entry.type === "message" && (entry.message?.role === "user" || entry.message?.role === "assistant");
}

export function sessionTreeEntryLabel(entry: SessionEntry): string {
  if (entry.type !== "message" || !entry.message) {
    return "";
  }
  return truncateTreeLabel(messageEntryLabel(entry.message).replace(/\s+/g, " ").trim());
}

export function sessionTreeRowPrefix(row: SessionTreeRow): string {
  const connector = row.showConnector && !row.isVirtualRootChild;
  const connectorPosition = connector ? row.displayIndent - 1 : -1;
  const prefixChars: string[] = [];

  for (let index = 0; index < row.displayIndent * 3; index++) {
    const level = Math.floor(index / 3);
    const posInLevel = index % 3;
    const gutter = row.gutters.find((item) => item.position === level);

    if (gutter) {
      prefixChars.push(posInLevel === 0 && gutter.show ? "│" : " ");
      continue;
    }

    if (connector && level === connectorPosition) {
      if (posInLevel === 0) {
        prefixChars.push(row.isLast ? "└" : "├");
      } else if (posInLevel === 1) {
        prefixChars.push("─");
      } else {
        prefixChars.push(" ");
      }
      continue;
    }

    prefixChars.push(" ");
  }

  return prefixChars.join("");
}

function buildRawTree(entries: TreeEntry[], activeLeafId: string) {
  const byID = new Map<string, RawNode>();
  const byEntry = new Map<string, TreeEntry>();
  const nodes: RawNode[] = [];

  entries.forEach((entry, sourceIndex) => {
    if (!entry.id) return;
    const node: RawNode = {
      entry,
      id: entry.id,
      label: sessionTreeEntryLabel(entry),
      sourceIndex,
      parentId: entry.parentId || "",
      children: [],
      containsActive: entry.id === activeLeafId,
    };
    byID.set(node.id, node);
    byEntry.set(node.id, entry);
    nodes.push(node);
  });

  for (const node of nodes) {
    if (node.parentId && node.parentId !== node.id) {
      const parent = byID.get(node.parentId);
      if (parent) {
        parent.children.push(node);
      }
    }
  }

  const activePathIDs = activePath(activeLeafId, byEntry);
  for (let index = nodes.length - 1; index >= 0; index--) {
    const node = nodes[index];
    node.containsActive = activePathIDs.has(node.id) || node.children.some((child) => child.containsActive);
  }

  return { byID, byEntry, nodes, activePathIDs };
}

function orderVisibleSiblings(children: string[], nodes: Map<string, RawNode>): string[] {
  return [...children].sort((leftID, rightID) => {
    const left = nodes.get(leftID);
    const right = nodes.get(rightID);
    const leftActive = left?.containsActive ? 1 : 0;
    const rightActive = right?.containsActive ? 1 : 0;
    if (leftActive !== rightActive) return rightActive - leftActive;
    return (left?.sourceIndex ?? 0) - (right?.sourceIndex ?? 0);
  });
}

function activePath(entryID: string, entries: Map<string, TreeEntry>): Set<string> {
  const out = new Set<string>();
  let currentID = entryID;
  while (currentID) {
    const entry = entries.get(currentID);
    if (!entry) break;
    out.add(currentID);
    currentID = entry.parentId || "";
  }
  return out;
}

function nearestVisibleAncestorID(
  parentID: string,
  entries: Map<string, RawNode>,
  visibleIDs: Set<string>,
): string | null {
  let currentID = parentID;
  while (currentID) {
    if (visibleIDs.has(currentID)) return currentID;
    const entry = entries.get(currentID);
    currentID = entry?.parentId || "";
  }
  return null;
}

function nearestVisibleEntryID(
  entryID: string,
  entries: Map<string, TreeEntry>,
  visibleIDs: Set<string>,
): string {
  let currentID = entryID;
  while (currentID) {
    if (visibleIDs.has(currentID)) return currentID;
    const entry = entries.get(currentID);
    currentID = entry?.parentId || "";
  }
  return "";
}

function messageEntryLabel(message: AgentMessage): string {
  if (message.role === "user") {
    return `user: ${messageText(message) || "(empty)"}`;
  }
  if (message.role !== "assistant") {
    return "";
  }
  const text = messageText(message);
  if (text) return `assistant: ${text}`;
  if (message.errorMessage) return `assistant: ${message.errorMessage}`;
  if (message.stopReason === "aborted") return "assistant: (aborted)";
  const toolNames = message.content
    .flatMap((block) => (block.type === "toolCall" ? [block.name] : []))
    .join(", ");
  return `assistant: ${toolNames || "回复中"}`;
}

function messageText(message: AgentMessage): string {
  return (message.content || [])
    .flatMap((block) => (block.type === "text" ? [block.text] : []))
    .join("");
}

function truncateTreeLabel(text: string): string {
  if (text.length <= TREE_LABEL_MAX_CHARS) return text;
  return `${text.slice(0, TREE_LABEL_MAX_CHARS - 1)}…`;
}

function mergeRealtimeEntries(
  entries: SessionEntry[],
  messages: AgentMessage[],
  activeLeafId: string,
): { entries: TreeEntry[]; activeLeafId: string } {
  if (messages.length === 0) {
    return { entries: entries as TreeEntry[], activeLeafId };
  }

  const pathMessages = contextMessagesForPath(entries, activeLeafId);
  let pathIndex = 0;
  let parentID = activeLeafId;
  const out: TreeEntry[] = [...entries];
  let realtimeLeafID = activeLeafId;

  for (const [messageIndex, message] of messages.entries()) {
    const fingerprint = messageFingerprint(message);
    const pathMessage = pathMessages[pathIndex];
    if (pathMessage && pathMessage.fingerprint === fingerprint) {
      parentID = pathMessage.entry.id || parentID;
      realtimeLeafID = parentID;
      pathIndex++;
      continue;
    }

    const id = realtimeEntryID(message, messageIndex);
    out.push({
      type: "message",
      version: 1,
      id,
      parentId: parentID,
      timestamp: new Date((message.timestamp || Date.now()) + messageIndex).toISOString(),
      message,
      transient: true,
    });
    parentID = id;
    realtimeLeafID = id;
  }

  return { entries: out, activeLeafId: realtimeLeafID };
}

function contextMessagesForPath(entries: SessionEntry[], activeLeafId: string): Array<{ entry: SessionEntry; fingerprint: string }> {
  const byID = new Map(entries.flatMap((entry) => (entry.id ? [[entry.id, entry] as const] : [])));
  const path: SessionEntry[] = [];
  const seen = new Set<string>();
  let currentID = activeLeafId;
  while (currentID && !seen.has(currentID)) {
    seen.add(currentID);
    const entry = byID.get(currentID);
    if (!entry) break;
    path.unshift(entry);
    currentID = entry.parentId || "";
  }
  return path.flatMap((entry) => {
    const fingerprint = contextEntryFingerprint(entry);
    return fingerprint ? [{ entry, fingerprint }] : [];
  });
}

function contextEntryFingerprint(entry: SessionEntry): string {
  if (entry.type === "message" && entry.message) {
    return messageFingerprint(entry.message);
  }
  if (entry.type === "branch_summary") {
    return `user|Branch summary:\n\n${entry.summary || ""}`;
  }
  if (entry.type === "compaction") {
    return `user|Context summary:\n\n${entry.summary || ""}`;
  }
  return "";
}

function messageFingerprint(message: AgentMessage): string {
  if (message.role === "toolResult") {
    return `toolResult|${message.toolCallId}|${message.toolName}|${messageText(message)}`;
  }
  const toolCalls = message.role === "assistant"
    ? message.content.flatMap((block) => (block.type === "toolCall" ? [`${block.id}:${block.name}:${JSON.stringify(block.arguments)}`] : []))
    : [];
  return `${message.role}|${messageText(message)}|${toolCalls.join(",")}`;
}

function realtimeEntryID(message: AgentMessage, index: number): string {
  if (message.role === "toolResult") {
    return `runtime-${index}-tool-${message.toolCallId}`;
  }
  return `runtime-${index}-${message.role}-${message.timestamp || "live"}`;
}

function isFoldable(
  entryID: string,
  visibleParentMap: Map<string, string | null>,
  visibleChildrenMap: Map<string | null, string[]>,
): boolean {
  const children = visibleChildrenMap.get(entryID) || [];
  if (children.length === 0) return false;
  const parentID = visibleParentMap.get(entryID);
  if (parentID === null || parentID === undefined) return true;
  const siblings = visibleChildrenMap.get(parentID) || [];
  return siblings.length > 1 || children.length > 1;
}
