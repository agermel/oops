import React from "react";
import {
  AlertTriangle,
  Bot,
  Check,
  ChevronDown,
  ChevronRight,
  GitBranch,
  Hammer,
  MessageSquare,
  Pencil,
  Plus,
  Send,
  Square,
  Trash2,
  X,
  User,
  Wrench,
} from "lucide-react";
import type {
  AgentEvent,
  AgentMessage,
  ContentBlock,
  SessionInfo,
  SessionResponse,
  ToolCallContent,
  ToolDefinition,
  ToolResult,
  Skill,
} from "../types";
import { CHAT_MAX_SESSION_TABS } from "../types";
import { getErrorMessage } from "../lib/api";
import { useToolToggle, useTools, type ToolItem, type ToolsData } from "../hooks/useTools";
import { messageKey, textFromContent, toolCallsFromMessage } from "../lib/session";
import {
  buildSessionTreeDisplay,
  sessionTreeRowPrefix,
  type SessionTreeDisplay,
  type SessionTreeRow,
} from "../lib/sessionTree";
import { ToggleSwitch } from "./ToggleSwitch";
import { StreamingText } from "./StreamingText";
import { FormInput } from "./ui/FormInput";
import { Button } from "./ui/Button";
import {
  applySkillSuggestion,
  handleSkillSuggestionKey,
  parseSkillInvocationSummary,
  skillInvocationDisplayText,
  skillSuggestions,
  type SkillSuggestion,
} from "../lib/skillSlashCommand";

const MESSAGE_AUTO_SCROLL_THRESHOLD_PX = 48;

export function ChatView({
  session,
  chatInput,
  chatLoading,
  chatError,
  sessions,
  skills,
  onInputChange,
  onSend,
  onAbort,
  onClear,
  onNewChat,
  onSelectSession,
  onRenameSession,
  onDeleteSession,
  onSelectLeaf,
}: {
  session: SessionResponse;
  chatInput: string;
  chatLoading: boolean;
  chatError: string;
  sessions: SessionInfo[];
  skills: Skill[];
  onInputChange: (value: string) => void;
  onSend: () => void;
  onAbort: () => void;
  onClear: () => void;
  onNewChat: () => void;
  onSelectSession: (id: string) => void;
  onRenameSession: (id: string, title: string) => Promise<void> | void;
  onDeleteSession: (id: string) => Promise<void> | void;
  onSelectLeaf: (leafId: string) => void;
}) {
  const sessionId = session.sessionId;
  const messages = session.messages || [];
  const events = session.events || [];
  const messageListRef = React.useRef<HTMLDivElement>(null);
  const shouldFollowMessagesRef = React.useRef(true);
  const [collapsedTreeIds, setCollapsedTreeIds] = React.useState<Set<string>>(() => new Set());
  const inputRef = React.useRef<HTMLTextAreaElement>(null);
  const [skillMenuOpen, setSkillMenuOpen] = React.useState(false);
  const [activeSkillIndex, setActiveSkillIndex] = React.useState(0);
  const activeSkillIndexRef = React.useRef(0);
  const tree = React.useMemo(
    () => buildSessionTreeDisplay(session.entries || [], session.leafId || "", {
      messages,
      collapsedIds: collapsedTreeIds,
    }),
    [collapsedTreeIds, messages, session.entries, session.leafId],
  );
  const toolExecutions = React.useMemo(() => currentToolExecutions(events, messages), [events, messages]);
  const { data: toolsData, isLoading: toolsLoading, error: toolsError } = useTools();
  const toolCount = toolsData ? toolInventoryCount(toolsData) : session.tools?.length || 0;
  const sessionTabs = React.useMemo(
    () => buildSessionTabs(sessions, sessionId, messages),
    [messages, sessionId, sessions],
  );
  const toggleTreeCollapse = React.useCallback((entryId: string) => {
    setCollapsedTreeIds((current) => {
      const next = new Set(current);
      if (next.has(entryId)) {
        next.delete(entryId);
      } else {
        next.add(entryId);
      }
      return next;
    });
  }, []);
  const sidebarTabs = React.useMemo(
    () => [
      {
        id: "sessions",
        label: "会话",
        icon: <MessageSquare size={15} />,
        badge: sessionTabs.length,
        content: (
          <SessionTabsPanel
            tabs={sessionTabs}
            disabled={chatLoading}
            onSelectSession={onSelectSession}
            onRenameSession={onRenameSession}
            onDeleteSession={onDeleteSession}
          />
        ),
      },
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
        badge: toolExecutions.length,
        content: <ToolExecutionList executions={toolExecutions} />,
      },
      {
        id: "tree",
        label: "会话树",
        icon: <GitBranch size={15} />,
        badge: tree.visibleCount,
        content: (
          <SessionTree
            tree={tree}
            disabled={chatLoading}
            onSelectLeaf={onSelectLeaf}
            onToggleCollapse={toggleTreeCollapse}
          />
        ),
      },
    ],
    [
      chatLoading,
      onDeleteSession,
      onSelectLeaf,
      onSelectSession,
      onRenameSession,
      toggleTreeCollapse,
      session.tools,
      sessionTabs,
      toolCount,
      toolExecutions,
      toolsData,
      toolsError,
      toolsLoading,
      tree,
    ],
  );
  const [activeSidebarTab, setActiveSidebarTab] = React.useState(sidebarTabs[0].id);
  const activeSidebarPanel = sidebarTabs.find((tab) => tab.id === activeSidebarTab) || sidebarTabs[0];
  const hasContent = messages.length > 0 || events.length > 0;
  const lastMessage = messages[messages.length - 1];
  const showAssistantThinking = chatLoading && lastMessage?.role !== "assistant";
  const skillMenuItems = React.useMemo(() => {
    if (!skillMenuOpen || chatLoading) return [];
    return skillSuggestions(chatInput, skills);
  }, [chatInput, chatLoading, skillMenuOpen, skills]);

  const scrollMessagesToBottom = React.useCallback(() => {
    const list = messageListRef.current;
    if (!list) return;
    list.scrollTop = list.scrollHeight;
  }, []);

  React.useLayoutEffect(() => {
    shouldFollowMessagesRef.current = true;
    scrollMessagesToBottom();
  }, [scrollMessagesToBottom, sessionId]);

  React.useLayoutEffect(() => {
    if (shouldFollowMessagesRef.current) {
      scrollMessagesToBottom();
    }
  }, [chatError, chatLoading, events.length, messages, scrollMessagesToBottom]);

  function handleMessageListScroll(event: React.UIEvent<HTMLDivElement>) {
    shouldFollowMessagesRef.current = isNearScrollBottom(event.currentTarget);
  }

  function handleInputChange(value: string) {
    onInputChange(value);
    const hasSuggestions = skillSuggestions(value, skills).length > 0;
    setSkillMenuOpen(hasSuggestions);
    setSkillSelection(0);
  }

  function applySuggestion(suggestion: SkillSuggestion) {
    const applied = applySkillSuggestion(chatInput, suggestion);
    onInputChange(applied.value);
    setSkillMenuOpen(false);
    requestAnimationFrame(() => {
      inputRef.current?.focus();
      inputRef.current?.setSelectionRange(applied.cursor, applied.cursor);
    });
  }

  function setSkillSelection(index: number) {
    activeSkillIndexRef.current = index;
    setActiveSkillIndex(index);
  }

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

      <div className="agent-runtime-body">
        <div
          ref={messageListRef}
          className="chat-body agent-message-list"
          role="log"
          aria-live="polite"
          onScroll={handleMessageListScroll}
        >
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
          {showAssistantThinking && (
            <div className="chat-msg assistant">
              <div className="chat-avatar"><Bot size={16} /></div>
              <div className="chat-content chat-thinking">思考中…</div>
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
                  <span className="runtime-sidebar-tab-head">
                    {tab.icon}
                    {tab.badge > 0 && <small>{tab.badge}</small>}
                  </span>
                  <span className="runtime-sidebar-tab-label">{tab.label}</span>
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
            ref={inputRef}
            multiline
            className="chat-input"
            placeholder="输入问题，Enter 发送，Shift+Enter 换行"
            value={chatInput}
            onChange={(e) => handleInputChange(e.target.value)}
            onKeyDown={(e: React.KeyboardEvent) => {
              if (skillMenuItems.length > 0) {
                const result = handleSkillSuggestionKey(e.key, activeSkillIndexRef.current, skillMenuItems.length, e.nativeEvent.isComposing);
                if (result.action === "select") {
                  e.preventDefault();
                  applySuggestion(skillMenuItems[result.nextIndex]);
                  return;
                }
                if (result.action === "close") {
                  e.preventDefault();
                  setSkillMenuOpen(false);
                  return;
                }
                if (result.action === "move") {
                  e.preventDefault();
                  setSkillSelection(result.nextIndex);
                  return;
                }
              }
              if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
                e.preventDefault();
                onSend();
              }
            }}
            disabled={chatLoading}
          />
          {skillMenuItems.length > 0 && (
            <div className="skill-command-menu" role="listbox" aria-label="Skill suggestions">
              {skillMenuItems.map((item, index) => (
                <button
                  key={item.name}
                  type="button"
                  role="option"
                  aria-selected={index === activeSkillIndex}
                  className={`skill-command-item ${index === activeSkillIndex ? "active" : ""}`}
                  onMouseDown={(event) => {
                    event.preventDefault();
                    applySuggestion(item);
                  }}
                >
                  <span>{item.command}</span>
                  {item.description && <small>{item.description}</small>}
                </button>
              ))}
            </div>
          )}
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
    const text = textFromContent(message.content);
    const skillSummary = parseSkillInvocationSummary(text);
    return (
      <div className="chat-msg user">
        <div className="chat-avatar"><User size={16} /></div>
        <div className="chat-content">
          {skillSummary ? (
            <div className="skill-invocation-summary">
              <span>skill:{skillSummary.name}</span>
              {skillSummary.instructions && <p>{skillSummary.instructions}</p>}
            </div>
          ) : text}
        </div>
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
  const [expandedGroups, setExpandedGroups] = React.useState<Record<string, boolean>>({});
  const groups = toolGroups(inventory);
  const hasGroups = groups.length > 0;
  const errorMessage = error ? getErrorMessage(error, "读取工具列表失败") : "";

  function toggleGroup(id: string) {
    setExpandedGroups((prev) => ({ ...prev, [id]: !(prev[id] ?? false) }));
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

function ToolExecutionList({ executions }: { executions: ToolExecutionState[] }) {
  const [expandedExecutions, setExpandedExecutions] = React.useState<Record<string, boolean>>({});

  function toggleExecution(execution: ToolExecutionState) {
    setExpandedExecutions((prev) => ({
      ...prev,
      [execution.id]: !(prev[execution.id] ?? defaultExecutionExpanded(execution)),
    }));
  }

  if (executions.length === 0) return <div className="runtime-empty">暂无执行</div>;
  return (
    <div className="runtime-execution-list">
      {executions.map((execution, index) => {
        const expanded = expandedExecutions[execution.id] ?? defaultExecutionExpanded(execution);
        const panelID = `runtime-execution-${index}`;
        return (
          <section key={execution.id} className={`runtime-execution-item ${execution.error ? "runtime-execution-error" : ""}`}>
            <button
              type="button"
              className="runtime-execution-header"
              aria-expanded={expanded}
              aria-controls={panelID}
              onClick={() => toggleExecution(execution)}
            >
              {expanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
              <span title={execution.name}>{execution.name}</span>
              <small>{executionStatusLabel(execution)}</small>
            </button>
            {expanded && (
              <div id={panelID} className="runtime-execution-body">
                <ExecutionDetailBlock title="Input" value={formatJSON(execution.args ?? {})} />
                {execution.updates.length > 0 && <ExecutionDetailBlock title="Updates" value={execution.updates.join("")} />}
                <ExecutionDetailBlock title="Output" value={toolResultText(execution.result) || "等待输出"} muted={!execution.result} />
              </div>
            )}
          </section>
        );
      })}
    </div>
  );
}

function ExecutionDetailBlock({ title, value, muted = false }: { title: string; value: string; muted?: boolean }) {
  return (
    <div className="runtime-execution-block">
      <span>{title}</span>
      <pre className={`runtime-execution-pre ${muted ? "muted" : ""}`}>{value}</pre>
    </div>
  );
}

function SessionTabsPanel({
  tabs,
  disabled,
  onSelectSession,
  onRenameSession,
  onDeleteSession,
}: {
  tabs: SessionTab[];
  disabled: boolean;
  onSelectSession: (id: string) => void;
  onRenameSession: (id: string, title: string) => Promise<void> | void;
  onDeleteSession: (id: string) => Promise<void> | void;
}) {
  const [editingID, setEditingID] = React.useState("");
  const [draftTitle, setDraftTitle] = React.useState("");
  const [savingID, setSavingID] = React.useState("");
  const [deletingID, setDeletingID] = React.useState("");
  const inputRef = React.useRef<HTMLInputElement>(null);

  React.useEffect(() => {
    if (!editingID) return;
    inputRef.current?.focus();
    inputRef.current?.select();
  }, [editingID]);

  function startEdit(tab: SessionTab) {
    setEditingID(tab.id);
    setDraftTitle(tab.title);
  }

  async function saveTitle(tab: SessionTab) {
    const nextTitle = draftTitle.trim();
    if (!nextTitle || nextTitle === tab.title) {
      setEditingID("");
      return;
    }
    setSavingID(tab.id);
    try {
      await onRenameSession(tab.id, nextTitle);
      setEditingID("");
    } catch {
      undefined;
    } finally {
      setSavingID("");
    }
  }

  async function deleteTab(tab: SessionTab) {
    if (!window.confirm(`删除会话「${tab.title}」？`)) return;
    setDeletingID(tab.id);
    try {
      await onDeleteSession(tab.id);
    } catch {
      undefined;
    } finally {
      setDeletingID("");
    }
  }

  if (tabs.length === 0) return <div className="runtime-empty">暂无历史会话</div>;
  return (
    <div className="runtime-session-tabs" role="tablist" aria-label="历史会话">
      {tabs.slice(0, CHAT_MAX_SESSION_TABS).map((tab) => {
        const editing = editingID === tab.id;
        const busy = savingID === tab.id || deletingID === tab.id;
        return (
          <div key={tab.id} className={`runtime-session-tab ${tab.active ? "active" : ""}`}>
            {editing ? (
              <div className="runtime-session-edit">
                <input
                  ref={inputRef}
                  value={draftTitle}
                  maxLength={120}
                  disabled={disabled || busy}
                  onChange={(event) => setDraftTitle(event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === "Enter") void saveTitle(tab);
                    if (event.key === "Escape") setEditingID("");
                  }}
                />
                <button type="button" title="保存" disabled={disabled || busy || !draftTitle.trim()} onClick={() => void saveTitle(tab)}>
                  <Check size={14} />
                </button>
                <button type="button" title="取消" disabled={busy} onClick={() => setEditingID("")}>
                  <X size={14} />
                </button>
              </div>
            ) : (
              <>
                <button
                  type="button"
                  role="tab"
                  aria-selected={tab.active}
                  className="runtime-session-main"
                  title={tab.title}
                  onClick={() => onSelectSession(tab.id)}
                  disabled={disabled || tab.active || busy}
                >
                  <span>{tab.title}</span>
                  <small>{tab.meta}</small>
                </button>
                <div className="runtime-session-actions">
                  <button type="button" title="改标题" disabled={disabled || busy} onClick={() => startEdit(tab)}>
                    <Pencil size={13} />
                  </button>
                  <button type="button" title="删除" disabled={disabled || busy} onClick={() => void deleteTab(tab)}>
                    <Trash2 size={13} />
                  </button>
                </div>
              </>
            )}
          </div>
        );
      })}
    </div>
  );
}

function SessionTree({
  tree,
  disabled,
  onSelectLeaf,
  onToggleCollapse,
}: {
  tree: SessionTreeDisplay;
  disabled: boolean;
  onSelectLeaf: (leafId: string) => void;
  onToggleCollapse: (entryId: string) => void;
}) {
  const rows = tree.rows;
  const treeShellRef = React.useRef<HTMLDivElement>(null);
  const shouldFollowTreeRef = React.useRef(true);

  const scrollTreeToBottom = React.useCallback(() => {
    const scrollContainer = treeScrollContainer(treeShellRef.current);
    if (!scrollContainer) return;
    scrollContainer.scrollTop = scrollContainer.scrollHeight;
  }, []);

  React.useLayoutEffect(() => {
    shouldFollowTreeRef.current = true;
    scrollTreeToBottom();
  }, [scrollTreeToBottom]);

  React.useLayoutEffect(() => {
    if (shouldFollowTreeRef.current) {
      scrollTreeToBottom();
    }
  }, [rows, scrollTreeToBottom, tree.activeEntryId]);

  React.useEffect(() => {
    const scrollContainer = treeScrollContainer(treeShellRef.current);
    if (!scrollContainer) return undefined;
    const handleScroll = () => {
      shouldFollowTreeRef.current = isNearScrollBottom(scrollContainer);
    };
    handleScroll();
    scrollContainer.addEventListener("scroll", handleScroll, { passive: true });
    return () => scrollContainer.removeEventListener("scroll", handleScroll);
  }, []);

  return (
    <div className="runtime-tree-shell" ref={treeShellRef}>
      {rows.length === 0 ? (
        <div className="runtime-empty">暂无对话节点</div>
      ) : (
        <div className="runtime-tree">
          {rows.map((row) => (
            <TreeRowView
              key={row.id}
              row={row}
              activeEntryId={tree.activeEntryId}
              disabled={disabled}
              onSelectLeaf={onSelectLeaf}
              onToggleCollapse={onToggleCollapse}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function TreeRowView({
  row,
  activeEntryId,
  disabled,
  onSelectLeaf,
  onToggleCollapse,
}: {
  row: SessionTreeRow;
  activeEntryId: string;
  disabled: boolean;
  onSelectLeaf: (leafId: string) => void;
  onToggleCollapse: (entryId: string) => void;
}) {
  const id = row.id;
  const active = Boolean(activeEntryId && activeEntryId === id);
  const prefix = sessionTreeRowPrefix(row);
  return (
    <div className={`runtime-tree-row ${active ? "active" : ""} ${row.transient ? "transient" : ""}`} title={row.label}>
      <span className="runtime-tree-prefix" aria-hidden="true">{prefix}</span>
      <span className={`runtime-tree-path-marker ${row.isActivePath ? "active" : ""}`} aria-hidden="true">
        {row.isActivePath ? "•" : ""}
      </span>
      <button
        type="button"
        className="runtime-tree-fold"
        disabled={!row.foldable}
        aria-label={row.collapsed ? "展开分支" : "折叠分支"}
        onClick={() => onToggleCollapse(id)}
      >
        {row.foldable ? (row.collapsed ? <ChevronRight size={13} /> : <ChevronDown size={13} />) : null}
      </button>
      <button
        type="button"
        className="runtime-tree-select"
        disabled={disabled || !id || active || row.transient}
        onClick={() => id && onSelectLeaf(id)}
      >
        <span className="runtime-tree-text">{row.label}</span>
      </button>
    </div>
  );
}

type ToolExecutionState = {
  id: string;
  name: string;
  done: boolean;
  error: boolean;
  args?: Record<string, unknown>;
  updates: string[];
  result?: ToolResult;
};

type SessionTab = {
  id: string;
  title: string;
  meta: string;
  active: boolean;
};

function currentToolExecutions(events: AgentEvent[], messages: AgentMessage[]): ToolExecutionState[] {
  const states = new Map<string, ToolExecutionState>();
  for (const message of messages) {
    if (message.role === "assistant") {
      for (const call of toolCallsFromMessage(message)) {
        const previous = states.get(call.id);
        states.set(call.id, {
          id: call.id,
          name: call.name,
          done: previous?.done ?? false,
          error: previous?.error ?? false,
          args: call.arguments,
          updates: previous?.updates ?? [],
          result: previous?.result,
        });
      }
    }
    if (message.role === "toolResult") {
      const previous = states.get(message.toolCallId);
      states.set(message.toolCallId, {
        id: message.toolCallId,
        name: message.toolName,
        done: true,
        error: message.isError,
        args: previous?.args,
        updates: previous?.updates ?? [],
        result: {
          content: message.content,
          details: message.details,
        },
      });
    }
  }
  for (const event of events) {
    if (event.type === "message_end" && event.message?.role === "assistant") {
      for (const call of toolCallsFromMessage(event.message)) {
        const previous = states.get(call.id);
        states.set(call.id, {
          id: call.id,
          name: call.name,
          done: previous?.done ?? false,
          error: previous?.error ?? false,
          args: call.arguments,
          updates: previous?.updates ?? [],
          result: previous?.result,
        });
      }
    }
    if (event.type === "tool_execution_start") {
      const previous = states.get(event.toolCallId);
      states.set(event.toolCallId, {
        id: event.toolCallId,
        name: event.toolName,
        done: false,
        error: false,
        args: event.args,
        updates: previous?.updates ?? [],
        result: previous?.result,
      });
    }
    if (event.type === "tool_execution_update") {
      const previous = states.get(event.toolCallId);
      states.set(event.toolCallId, {
        id: event.toolCallId,
        name: event.toolName,
        done: previous?.done ?? false,
        error: previous?.error ?? false,
        args: previous?.args,
        updates: [...(previous?.updates ?? []), event.delta],
        result: previous?.result,
      });
    }
    if (event.type === "tool_execution_end") {
      const previous = states.get(event.toolCallId);
      states.set(event.toolCallId, {
        id: event.toolCallId,
        name: event.toolName,
        done: true,
        error: event.isError,
        args: previous?.args,
        updates: previous?.updates ?? [],
        result: event.result,
      });
    }
  }
  return Array.from(states.values()).slice(-12);
}

function toolInventoryCount(inventory: ToolsData): number {
  return inventory.native.length + Object.values(inventory.mcp).reduce((total, tools) => total + tools.length, 0);
}

function enabledToolCount(tools: ToolItem[]): number {
  return tools.filter((tool) => tool.enabled).length;
}

function buildSessionTabs(sessions: SessionInfo[], sessionId: string, messages: AgentMessage[]): SessionTab[] {
  const byID = new Map(sessions.map((session) => [session.id, session]));
  const activeSession = sessionId ? byID.get(sessionId) : undefined;
  const activeTab = sessionId
    ? [sessionTabFromInfo(
        activeSession || {
          id: sessionId,
          title: "",
          summary: firstUserMessageSummary(messages),
          messageCount: messages.length,
          createdAt: Date.now(),
          updatedAt: Date.now(),
        },
        sessionId,
        messages,
      )]
    : [];
  const historyTabs = sessions
    .filter((session) => session.id !== sessionId)
    .map((session) => sessionTabFromInfo(session, sessionId, messages));
  return [...activeTab, ...historyTabs].filter((tab) => tab.id);
}

function sessionTabFromInfo(session: SessionInfo, activeSessionId: string, activeMessages: AgentMessage[]): SessionTab {
  const active = session.id === activeSessionId;
  const title = shortSessionTitle(
    active
      ? firstNonEmpty(session.title, firstUserMessageSummary(activeMessages), session.summary)
      : firstNonEmpty(session.title, session.summary),
    session.id,
  );
  const count = session.messageCount || (active ? activeMessages.length : 0);
  const parts = [`${count} 条`];
  const age = relativeSessionTime(session.updatedAt || session.createdAt);
  if (age) {
    parts.push(age);
  }
  if (active) {
    parts.unshift("当前");
  }
  return { id: session.id, title, meta: parts.join(" · "), active };
}

function firstUserMessageSummary(messages: AgentMessage[]): string {
  const user = messages.find((message) => message.role === "user");
  return user ? skillInvocationDisplayText(textFromContent(user.content)) : "";
}

function shortSessionTitle(value: string | undefined, fallbackID: string): string {
  const text = firstNonEmpty(value).replace(/\s+/g, " ").trim();
  const fallback = fallbackID ? `会话 ${fallbackID.slice(-8)}` : "新会话";
  return truncatePlainText(text || fallback, 36);
}

function truncatePlainText(value: string, maxLength: number): string {
  if (value.length <= maxLength) return value;
  return `${value.slice(0, maxLength - 1)}…`;
}

function relativeSessionTime(timestamp: number | undefined): string {
  if (!timestamp) return "";
  const diffMs = Math.max(0, Date.now() - timestamp);
  const minutes = Math.floor(diffMs / 60_000);
  const hours = Math.floor(diffMs / 3_600_000);
  const days = Math.floor(diffMs / 86_400_000);
  if (minutes < 1) return "刚刚";
  if (minutes < 60) return `${minutes} 分钟前`;
  if (hours < 24) return `${hours} 小时前`;
  if (days < 7) return `${days} 天前`;
  return new Date(timestamp).toLocaleDateString();
}

function firstNonEmpty(...values: Array<string | undefined>): string {
  return values.find((value) => value && value.trim()) || "";
}

function isNearScrollBottom(element: HTMLElement): boolean {
  return element.scrollHeight - element.scrollTop - element.clientHeight <= MESSAGE_AUTO_SCROLL_THRESHOLD_PX;
}

function treeScrollContainer(element: HTMLElement | null): HTMLElement | null {
  return element?.closest<HTMLElement>(".runtime-panel-section") || element;
}

function defaultExecutionExpanded(execution: ToolExecutionState): boolean {
  return !execution.done || execution.error;
}

function executionStatusLabel(execution: ToolExecutionState): string {
  if (!execution.done) return "运行中";
  return execution.error ? "错误" : "完成";
}

function toolResultText(result?: ToolResult): string {
  if (!result) return "";
  const content = result.content.map(toolResultContentText).filter(Boolean).join("\n");
  if (content) return content;
  if (result.details !== undefined) return formatJSON(result.details);
  return "";
}

function toolResultContentText(content: ToolResult["content"][number]): string {
  if (content.type === "text") return content.text;
  if (content.url) return content.url;
  if (content.data) return `[image ${content.mimeType || "image"}]`;
  return "";
}

function formatJSON(value: unknown): string {
  if (typeof value === "string") return formatText(value);
  try {
    return JSON.stringify(value, null, 2) || "";
  } catch {
    return String(value);
  }
}

function toolGroups(inventory?: ToolsData): Array<{ id: string; title: string; tools: ToolItem[]; defaultExpanded: boolean }> {
  if (!inventory) return [];
  const groups: Array<{ id: string; title: string; tools: ToolItem[]; defaultExpanded: boolean }> = [];
  if (inventory.native.length > 0) {
    groups.push({ id: "native", title: "内置工具", tools: inventory.native, defaultExpanded: false });
  }
  for (const [name, tools] of Object.entries(inventory.mcp)) {
    if (tools.length > 0) {
      groups.push({ id: `mcp-${encodeURIComponent(name)}`, title: `MCP: ${name}`, tools, defaultExpanded: false });
    }
  }
  return groups;
}

function formatText(text: string): string {
  try {
    return JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    return text;
  }
}
