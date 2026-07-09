import React from "react";
import {
  AlertTriangle,
  Bot,
  ChevronDown,
  ChevronRight,
  GitBranch,
  Hammer,
  MessageSquare,
  Plus,
  Send,
  Square,
  Trash2,
  User,
  Wrench,
} from "lucide-react";
import type {
  AgentEvent,
  AgentMessage,
  ContentBlock,
  SessionEntry,
  SessionInfo,
  SessionResponse,
  ToolCallContent,
  ToolDefinition,
} from "../types";
import { CHAT_MAX_SESSION_BADGES } from "../types";
import { getErrorMessage } from "../lib/api";
import { useToolToggle, useTools, type ToolItem, type ToolsData } from "../hooks/useTools";
import { messageKey, textFromContent, toolCallsFromMessage } from "../lib/session";
import { ToggleSwitch } from "./ToggleSwitch";
import { StreamingText } from "./StreamingText";
import { FormInput } from "./ui/FormInput";
import { Button } from "./ui/Button";

type TreeNode = {
  entry: SessionEntry;
  children: TreeNode[];
};

export function ChatView({
  session,
  chatInput,
  chatLoading,
  chatError,
  sessions,
  onInputChange,
  onSend,
  onAbort,
  onClear,
  onNewChat,
  onSelectSession,
  onSelectLeaf,
}: {
  session: SessionResponse;
  chatInput: string;
  chatLoading: boolean;
  chatError: string;
  sessions: SessionInfo[];
  onInputChange: (value: string) => void;
  onSend: () => void;
  onAbort: () => void;
  onClear: () => void;
  onNewChat: () => void;
  onSelectSession: (id: string) => void;
  onSelectLeaf: (leafId: string) => void;
}) {
  const sessionId = session.sessionId;
  const messages = session.messages || [];
  const events = session.events || [];
  const tree = React.useMemo(() => buildSessionTree(session.entries || []), [session.entries]);
  const runningTools = React.useMemo(() => currentToolStates(events), [events]);
  const { data: toolsData, isLoading: toolsLoading, error: toolsError } = useTools();
  const toolCount = toolsData ? toolInventoryCount(toolsData) : session.tools?.length || 0;
  const sidebarTabs = React.useMemo(
    () => [
      {
        id: "tools",
        label: "工具",
        icon: <Wrench size={15} />,
        badge: toolCount,
        content: (
          <RuntimeToolInventory
            inventory={toolsData}
            loading={toolsLoading}
            error={toolsError}
            fallbackTools={session.tools || []}
          />
        ),
      },
      {
        id: "executions",
        label: "执行",
        icon: <Hammer size={15} />,
        badge: runningTools.filter((state) => !state.done).length,
        content: <ToolExecutionList states={runningTools} />,
      },
      {
        id: "tree",
        label: "会话树",
        icon: <GitBranch size={15} />,
        badge: countTreeNodes(tree),
        content: <SessionTree nodes={tree} activeLeafId={session.leafId || ""} disabled={chatLoading} onSelectLeaf={onSelectLeaf} />,
      },
      {
        id: "events",
        label: "时间线",
        icon: <MessageSquare size={15} />,
        badge: events.length,
        content: <EventTimeline events={events} />,
      },
    ],
    [chatLoading, events, onSelectLeaf, runningTools, session.leafId, session.tools, toolCount, toolsData, toolsError, toolsLoading, tree],
  );
  const [activeSidebarTab, setActiveSidebarTab] = React.useState(sidebarTabs[0].id);
  const activeSidebarPanel = sidebarTabs.find((tab) => tab.id === activeSidebarTab) || sidebarTabs[0];
  const otherSessions = sessions.filter((s) => s.id !== sessionId);
  const hasContent = messages.length > 0 || events.length > 0;
  const lastMessage = messages[messages.length - 1];

  return (
    <section className="chat-panel agent-runtime-panel" id="chat-section">
      <div className="chat-header">
        <div className="chat-header-left">
          <Bot size={18} />
          <span>Agent Runtime</span>
          {sessionId && <span className="runtime-session-id">{sessionId.slice(-10)}</span>}
          {chatLoading && <span className="runtime-status">运行中</span>}
        </div>
        <span className="chat-header-actions">
          {chatLoading && (
            <button onClick={onAbort} title="停止运行" aria-label="停止运行">
              <Square size={15} />
            </button>
          )}
          <button onClick={onNewChat} title="新建对话" aria-label="新建对话">
            <Plus size={16} />
          </button>
          {hasContent && (
            <button onClick={onClear} title="清空当前对话" aria-label="清空当前对话">
              <Trash2 size={16} />
            </button>
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
              title={`${s.messageCount} 条消息`}
              onClick={() => onSelectSession(s.id)}
              disabled={chatLoading}
            >
              {(s.id || "").slice(-8)}
            </button>
          ))}
        </div>
      )}

      <div className="agent-runtime-body">
        <div className="chat-body agent-message-list" role="log" aria-live="polite">
          {messages.length === 0 && !chatLoading && (
            <div className="chat-empty">暂无消息</div>
          )}
          {messages.map((message, index) => (
            <AgentMessageView
              key={messageKey(message, index)}
              message={message}
              animate={chatLoading && index === messages.length - 1 && lastMessage?.role === "assistant"}
            />
          ))}
          {messages.length === 0 && chatLoading && (
            <div className="chat-msg assistant">
              <div className="chat-avatar"><Bot size={16} /></div>
              <div className="chat-content chat-thinking">运行中…</div>
            </div>
          )}
          {chatError && <div className="chat-error">{chatError}</div>}
        </div>

        <aside className="agent-runtime-sidebar" aria-label="运行状态侧边栏">
          <div className="runtime-sidebar-tabs" role="tablist" aria-label="运行状态">
            {sidebarTabs.map((tab) => {
              const active = tab.id === activeSidebarPanel.id;
              return (
                <button
                  key={tab.id}
                  type="button"
                  role="tab"
                  className={`runtime-sidebar-tab ${active ? "active" : ""}`}
                  aria-selected={active}
                  aria-controls={`runtime-sidebar-panel-${tab.id}`}
                  id={`runtime-sidebar-tab-${tab.id}`}
                  title={tab.label}
                  onClick={() => setActiveSidebarTab(tab.id)}
                >
                  {tab.icon}
                  <span>{tab.label}</span>
                  {tab.badge > 0 && <small>{tab.badge}</small>}
                </button>
              );
            })}
          </div>
          <RuntimePanel
            id={`runtime-sidebar-panel-${activeSidebarPanel.id}`}
            labelledBy={`runtime-sidebar-tab-${activeSidebarPanel.id}`}
            title={activeSidebarPanel.label}
            icon={activeSidebarPanel.icon}
          >
            {activeSidebarPanel.content}
          </RuntimePanel>
        </aside>
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
      </div>
    </section>
  );
}

function AgentMessageView({ message, animate }: { message: AgentMessage; animate: boolean }) {
  if (message.role === "user") {
    return (
      <div className="chat-msg user">
        <div className="chat-avatar"><User size={16} /></div>
        <div className="chat-content">{textFromContent(message.content)}</div>
      </div>
    );
  }
  if (message.role === "toolResult") {
    return (
      <div className={`runtime-message tool-result ${message.isError ? "tool-error" : ""}`}>
        <div className="runtime-message-title">
          {message.isError ? <AlertTriangle size={14} /> : <Hammer size={14} />}
          <span>{message.toolName}</span>
        </div>
        <pre>{formatText(textFromContent(message.content))}</pre>
      </div>
    );
  }
  return (
    <div className="chat-msg assistant">
      <div className="chat-avatar"><Bot size={16} /></div>
      <div className="chat-content">
        {message.content.map((content, index) => (
          <ContentView key={`${content.type}-${index}`} content={content} animate={animate} />
        ))}
        {message.errorMessage && <div className="chat-error">{message.errorMessage}</div>}
      </div>
    </div>
  );
}

function ContentView({ content, animate }: { content: ContentBlock; animate: boolean }) {
  if (content.type === "text") {
    return <StreamingText text={content.text} animate={animate} />;
  }
  if (content.type === "thinking") {
    return <div className="runtime-thinking"><StreamingText text={content.thinking} animate={animate} /></div>;
  }
  if (content.type === "toolCall") {
    return <ToolCallView call={content} />;
  }
  if (content.url) {
    return <div className="runtime-media-link">{content.url}</div>;
  }
  return null;
}

function ToolCallView({ call }: { call: ToolCallContent }) {
  return (
    <div className="runtime-tool-call">
      <Wrench size={14} />
      <span>{call.name}</span>
      <code>{JSON.stringify(call.arguments)}</code>
    </div>
  );
}

function RuntimePanel({
  id,
  labelledBy,
  title,
  icon,
  children,
}: {
  id: string;
  labelledBy: string;
  title: string;
  icon: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <section className="runtime-panel-section" role="tabpanel" id={id} aria-labelledby={labelledBy}>
      <h3>{icon}<span>{title}</span></h3>
      {children}
    </section>
  );
}

function ToolList({ tools }: { tools: ToolDefinition[] }) {
  if (tools.length === 0) return <div className="runtime-empty">暂无工具</div>;
  return (
    <div className="runtime-tool-list">
      {tools.map((tool) => (
        <div key={tool.name} className="runtime-tool-item">
          <span>{tool.name}</span>
          {tool.description && <small>{tool.description}</small>}
        </div>
      ))}
    </div>
  );
}

function RuntimeToolInventory({
  inventory,
  loading,
  error,
  fallbackTools,
}: {
  inventory?: ToolsData;
  loading: boolean;
  error: unknown;
  fallbackTools: ToolDefinition[];
}) {
  const toggleMutation = useToolToggle();
  const [expandedGroups, setExpandedGroups] = React.useState<Record<string, boolean>>({ native: true });
  const groups = toolGroups(inventory);
  const hasGroups = groups.length > 0;
  const errorMessage = error ? getErrorMessage(error, "读取工具列表失败") : "";

  function toggleGroup(id: string) {
    setExpandedGroups((prev) => ({ ...prev, [id]: !(prev[id] ?? id === "native") }));
  }

  if (loading && !inventory) {
    return <div className="runtime-empty">读取工具中…</div>;
  }

  if (!hasGroups) {
    return (
      <div className="runtime-tool-groups">
        {errorMessage && <div className="runtime-tool-error">{errorMessage}</div>}
        <ToolList tools={fallbackTools} />
      </div>
    );
  }

  return (
    <div className="runtime-tool-groups">
      {errorMessage && <div className="runtime-tool-error">{errorMessage}</div>}
      {groups.map((group) => {
        const expanded = expandedGroups[group.id] ?? group.defaultExpanded;
        const togglingToolName = toggleMutation.isPending ? toggleMutation.variables?.name : "";
        return (
          <ToolGroupSection
            key={group.id}
            id={group.id}
            title={group.title}
            tools={group.tools}
            expanded={expanded}
            onToggle={() => toggleGroup(group.id)}
            togglingToolName={togglingToolName}
            onToolToggle={(name, enabled) => toggleMutation.mutate({ name, enabled })}
          />
        );
      })}
    </div>
  );
}

function ToolGroupSection({
  id,
  title,
  tools,
  expanded,
  onToggle,
  togglingToolName,
  onToolToggle,
}: {
  id: string;
  title: string;
  tools: ToolItem[];
  expanded: boolean;
  onToggle: () => void;
  togglingToolName?: string;
  onToolToggle: (name: string, enabled: boolean) => void;
}) {
  const panelID = `runtime-tool-group-${id}`;
  const enabled = enabledToolCount(tools);
  return (
    <section className="runtime-tool-group">
      <button
        type="button"
        className="runtime-tool-group-header"
        aria-expanded={expanded}
        aria-controls={panelID}
        onClick={onToggle}
      >
        {expanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
        <span>{title}</span>
        <small>{enabled}/{tools.length}</small>
      </button>
      {expanded && (
        <div id={panelID} className="runtime-tool-group-body">
          {tools.map((tool) => (
            <RuntimeToolRow
              key={tool.name}
              tool={tool}
              toggling={togglingToolName === tool.name}
              onToggle={(enabled) => onToolToggle(tool.name, enabled)}
            />
          ))}
        </div>
      )}
    </section>
  );
}

function RuntimeToolRow({
  tool,
  toggling,
  onToggle,
}: {
  tool: ToolItem;
  toggling: boolean;
  onToggle: (enabled: boolean) => void;
}) {
  return (
    <div className={`runtime-tool-row ${!tool.enabled ? "tool-disabled" : ""}`}>
      <div className="runtime-tool-info">
        <code className="runtime-tool-name">{tool.name}</code>
        {tool.description && <span className="runtime-tool-desc">{tool.description}</span>}
      </div>
      <ToggleSwitch checked={tool.enabled} disabled={toggling} onChange={onToggle} />
    </div>
  );
}

function ToolExecutionList({ states }: { states: ToolState[] }) {
  if (states.length === 0) return <div className="runtime-empty">暂无执行</div>;
  return (
    <div className="runtime-event-list">
      {states.map((state) => (
        <div key={state.id} className={`runtime-event-row ${state.error ? "runtime-event-error" : ""}`}>
          <span>{state.name}</span>
          <small>{state.done ? (state.error ? "错误" : "完成") : "运行中"}</small>
        </div>
      ))}
    </div>
  );
}

function EventTimeline({ events }: { events: AgentEvent[] }) {
  if (events.length === 0) return <div className="runtime-empty">暂无事件</div>;
  return (
    <div className="runtime-event-list">
      {events.slice(-80).map((event, index) => (
        <div key={`${event.type}-${index}`} className="runtime-event-row">
          <span>{event.type}</span>
          <small>{eventSummary(event)}</small>
        </div>
      ))}
    </div>
  );
}

function SessionTree({
  nodes,
  activeLeafId,
  disabled,
  onSelectLeaf,
}: {
  nodes: TreeNode[];
  activeLeafId: string;
  disabled: boolean;
  onSelectLeaf: (leafId: string) => void;
}) {
  if (nodes.length === 0) return <div className="runtime-empty">暂无 entry</div>;
  return (
    <div className="runtime-tree">
      {nodes.map((node) => (
        <TreeNodeView
          key={node.entry.id || node.entry.timestamp || node.entry.type}
          node={node}
          activeLeafId={activeLeafId}
          disabled={disabled}
          onSelectLeaf={onSelectLeaf}
        />
      ))}
    </div>
  );
}

function TreeNodeView({
  node,
  activeLeafId,
  disabled,
  onSelectLeaf,
}: {
  node: TreeNode;
  activeLeafId: string;
  disabled: boolean;
  onSelectLeaf: (leafId: string) => void;
}) {
  const id = node.entry.id || "";
  const active = Boolean(activeLeafId && activeLeafId === id);
  return (
    <div className="runtime-tree-node">
      <button
        type="button"
        className={`runtime-tree-label ${active ? "active" : ""}`}
        disabled={disabled || !id || active}
        onClick={() => id && onSelectLeaf(id)}
      >
        <span>{entryLabel(node.entry)}</span>
      </button>
      {node.children.length > 0 && (
        <div className="runtime-tree-children">
          {node.children.map((child) => (
            <TreeNodeView
              key={child.entry.id || child.entry.timestamp || child.entry.type}
              node={child}
              activeLeafId={activeLeafId}
              disabled={disabled}
              onSelectLeaf={onSelectLeaf}
            />
          ))}
        </div>
      )}
    </div>
  );
}

type ToolState = {
  id: string;
  name: string;
  done: boolean;
  error: boolean;
};

function currentToolStates(events: AgentEvent[]): ToolState[] {
  const states = new Map<string, ToolState>();
  for (const event of events) {
    if (event.type === "message_end" && event.message?.role === "assistant") {
      for (const call of toolCallsFromMessage(event.message)) {
        states.set(call.id, { id: call.id, name: call.name, done: false, error: false });
      }
    }
    if (event.type === "tool_execution_start") {
      states.set(event.toolCallId, { id: event.toolCallId, name: event.toolName, done: false, error: false });
    }
    if (event.type === "tool_execution_end") {
      states.set(event.toolCallId, { id: event.toolCallId, name: event.toolName, done: true, error: event.isError });
    }
  }
  return Array.from(states.values()).slice(-12);
}

function buildSessionTree(entries: SessionEntry[]): TreeNode[] {
  const nodes = new Map<string, TreeNode>();
  const roots: TreeNode[] = [];
  for (const entry of entries) {
    if (!entry.id) continue;
    nodes.set(entry.id, { entry, children: [] });
  }
  for (const node of nodes.values()) {
    const parentID = node.entry.parentId;
    if (parentID && nodes.has(parentID)) {
      nodes.get(parentID)!.children.push(node);
    } else {
      roots.push(node);
    }
  }
  return roots;
}

function countTreeNodes(nodes: TreeNode[]): number {
  return nodes.reduce((total, node) => total + 1 + countTreeNodes(node.children), 0);
}

function toolInventoryCount(inventory: ToolsData): number {
  return inventory.native.length + Object.values(inventory.mcp).reduce((total, tools) => total + tools.length, 0);
}

function enabledToolCount(tools: ToolItem[]): number {
  return tools.filter((tool) => tool.enabled).length;
}

function toolGroups(inventory?: ToolsData): Array<{ id: string; title: string; tools: ToolItem[]; defaultExpanded: boolean }> {
  if (!inventory) return [];
  const groups: Array<{ id: string; title: string; tools: ToolItem[]; defaultExpanded: boolean }> = [];
  if (inventory.native.length > 0) {
    groups.push({ id: "native", title: "内置工具", tools: inventory.native, defaultExpanded: true });
  }
  for (const [name, tools] of Object.entries(inventory.mcp)) {
    if (tools.length > 0) {
      groups.push({ id: `mcp-${encodeURIComponent(name)}`, title: `MCP: ${name}`, tools, defaultExpanded: false });
    }
  }
  return groups;
}

function entryLabel(entry: SessionEntry): string {
  if (entry.type === "message" && entry.message) {
    return `${entry.message.role}: ${textFromContent(entry.message.content).slice(0, 36) || entry.message.role}`;
  }
  if (entry.type === "model_change") return `model: ${entry.model || ""}`;
  if (entry.type === "active_tools_change") return `tools: ${(entry.toolNames || []).join(", ")}`;
  if (entry.type === "compaction") return "compaction";
  if (entry.type === "leaf") return `leaf: ${entry.leafId || ""}`;
  return entry.type;
}

function eventSummary(event: AgentEvent): string {
  if ("turn" in event && event.turn) return `turn ${event.turn}`;
  if (event.type === "tool_execution_start" || event.type === "tool_execution_update" || event.type === "tool_execution_end") {
    return event.toolName;
  }
  if ((event.type === "message_start" || event.type === "message_update" || event.type === "message_end") && event.message) {
    return event.message.role;
  }
  return "";
}

function formatText(text: string): string {
  try {
    return JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    return text;
  }
}
