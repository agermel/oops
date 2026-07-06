import React from "react";
import { useQueryClient } from "@tanstack/react-query";
import type {
  MCPConnectionConfig,
  MCPConnectionStatus,
  MCPPrefill,
  MCPContainerBindingOption,
  DSNConfig,
} from "../types";
import { serviceLabel } from "../types";
import { apiRequest, getErrorMessage } from "../lib/api";
import type { MCPCredentials as Credentials } from "../lib/mcpConfig";
import {
  buildMCPConnectionConfig as formToConfig,
  emptyMCPCredentials as emptyCreds,
  joinHostPort,
  parseMCPConnectionCredentials as parseCredentials,
  stripGeneratedMCPArgs as stripGeneratedArgs,
  stripGeneratedMCPEnv as stripGeneratedEnv,
  validateMCPCredentials as validateCredentials,
} from "../lib/mcpConfig";
import { mcpConnectionPaths, serverPaths } from "../lib/paths";
import { queryKeys } from "../hooks/queries";
import { Modal } from "./Modal";
import { Button } from "./ui/Button";
import { FormInput } from "./ui/FormInput";

// ---- 类型默认值 ----
const typeDefaults: Record<string, { command: string; args: string[]; env: string[] }> = {
  mysql: {
    command: "./mcp-servers/mysql/mysql-mcp-server",
    args: [],
    env: [],
  },
  redis: {
    command: "./mcp-servers/redis/redis-mcp-server",
    args: [],
    env: [],
  },
  etcd: {
    command: "./mcp-servers/etcd/etcd-mcp-server",
    args: [],
    env: [],
  },
  postgres: {
    command: "uvx",
    args: ["--from", "mcp-server-postgres@latest", "mcp-server-postgres"],
    env: [],
  },
  elasticsearch: {
    command: "./mcp-servers/elasticsearch/elasticsearch-mcp-server",
    args: [],
    env: [],
  },
  kafka: {
    command: "./mcp-servers/kafka/kafka-mcp",
    args: [],
    env: [],
  },
  nacos: {
    command: "./mcp-servers/nacos/nacos-mcp-server",
    args: [],
    env: [],
  },
  other: {
    command: "",
    args: [],
    env: [],
  },
};

// 哪些类型显示连接参数字段
const typesWithCredentials = new Set(["mysql", "redis", "postgres", "etcd", "elasticsearch", "kafka", "nacos"]);

function parseArgLines(value: string): string[] {
  return value.split("\n").map((arg) => arg.trim()).filter(Boolean);
}

// 每种类型展示的连接参数字段（按序）。
interface CredentialField {
  key: keyof Credentials;
  label: string;
  /** 设为 true 时使用 password 输入框 */
  isPassword?: boolean;
  options?: Array<{ value: string; label: string }>;
}

const kafkaSecurityProtocolOptions = [
  { value: "sasl_plaintext", label: "SASL_PLAINTEXT" },
  { value: "sasl_ssl", label: "SASL_SSL" },
  { value: "plaintext", label: "PLAINTEXT" },
  { value: "ssl", label: "SSL" },
];

const kafkaSaslMechanismOptions = [
  { value: "PLAIN", label: "PLAIN" },
  { value: "SCRAM-SHA-256", label: "SCRAM-SHA-256" },
  { value: "SCRAM-SHA-512", label: "SCRAM-SHA-512" },
];

const typeCredentialFields: Record<string, CredentialField[]> = {
  mysql: [
    { key: "host", label: "主机" },
    { key: "port", label: "端口" },
    { key: "user", label: "用户" },
    { key: "password", label: "密码", isPassword: true },
    { key: "database", label: "数据库" },
  ],
  redis: [
    { key: "host", label: "主机" },
    { key: "port", label: "端口" },
    { key: "user", label: "用户（可选）" },
    { key: "password", label: "密码", isPassword: true },
    { key: "database", label: "DB 编号" },
  ],
  postgres: [
    { key: "host", label: "主机" },
    { key: "port", label: "端口" },
    { key: "user", label: "用户" },
    { key: "password", label: "密码", isPassword: true },
    { key: "database", label: "数据库" },
  ],
  etcd: [
    { key: "host", label: "端点" },
    { key: "user", label: "用户" },
    { key: "password", label: "密码", isPassword: true },
  ],
  elasticsearch: [
    { key: "host", label: "地址" },
    { key: "port", label: "端口" },
    { key: "user", label: "用户" },
    { key: "password", label: "密码", isPassword: true },
  ],
  kafka: [
    { key: "host", label: "Bootstrap Servers" },
    { key: "port", label: "端口" },
    { key: "user", label: "用户名 / API Key" },
    { key: "password", label: "密码 / API Secret", isPassword: true },
    { key: "securityProtocol", label: "Security Protocol", options: kafkaSecurityProtocolOptions },
    { key: "saslMechanism", label: "SASL Mechanism", options: kafkaSaslMechanismOptions },
  ],
  nacos: [
    { key: "host", label: "Nacos 地址" },
    { key: "port", label: "端口" },
    { key: "user", label: "用户名" },
    { key: "password", label: "密码", isPassword: true },
    { key: "database", label: "命名空间（可选）" },
  ],
};

function emptyForm(type?: string): MCPConnectionStatus {
  const t = type || "mysql";
  const defaults = typeDefaults[t] || typeDefaults.other;
  return {
    id: "",
    name: "",
    type: t,
    transport: "stdio",
    command: defaults.command,
    args: [...defaults.args],
    env: [...defaults.env],
    url: "",
    enabled: true,
    status: "stopped",
    toolCount: 0,
  };
}

function bindingOptionLabel(option: MCPContainerBindingOption): string {
  return [
    option.nodeletName,
    option.containerName,
    bindingServiceText(option),
    option.containerId.slice(0, 12),
  ].filter(Boolean).join(" · ");
}

function bindingOptionSearchText(option: MCPContainerBindingOption): string {
  return [
    option.containerName,
    option.nodeletName,
    bindingServiceText(option),
    option.containerId,
  ].filter(Boolean).join(" ").toLowerCase();
}

function bindingServiceText(option: MCPContainerBindingOption): string {
  return serviceLabel(option.serviceType);
}

function supportedMCPType(serviceType: string, currentType: string): string {
  if (typeDefaults[serviceType]) return serviceType;
  if (typeDefaults[currentType]) return currentType;
  return "other";
}

function dsnRecord(config?: DSNConfig): Record<string, string> {
  if (!config) return {};
  return { ...config.detected, ...config.merged };
}

function envFromDSN(type: string, dsn: Record<string, string>): string[] {
  const host = dsn.host || "";
  const port = dsn.port || "";
  const user = dsn.user || "";
  const password = dsn.password || "";
  const database = dsn.database || "";
  const raw = dsn.raw || "";

  if (type === "mysql") {
    if (raw) return [`MYSQL_DSN=${raw}`];
    if (!host) return [];
    return [`MYSQL_DSN=${user}:@tcp(${host}:${port || "3306"})/${database}?charset=utf8mb4`];
  }
  if (type === "redis") {
    let redisHost = host;
    let redisPort = port;
    let redisUser = user;
    let redisDatabase = database;
    let redisPassword = password;
    if (raw) {
      try {
        const url = new URL(raw);
        redisHost ||= url.hostname;
        redisPort ||= url.port || "6379";
        redisUser ||= decodeURIComponent(url.username || "");
        redisPassword ||= decodeURIComponent(url.password || "");
        redisDatabase ||= url.pathname.replace(/^\//, "");
      } catch {
        const match = raw.match(/^([^:]+):(\d+)$/);
        if (match) {
          redisHost ||= match[1];
          redisPort ||= match[2];
        }
      }
    }
    if (!redisHost && !redisPort && !redisUser && !redisDatabase && !redisPassword) return [];
    return [
      redisHost ? `REDIS_HOST=${redisHost}` : "",
      redisPort ? `REDIS_PORT=${redisPort}` : "",
      redisUser ? `REDIS_USERNAME=${redisUser}` : "",
      redisDatabase ? `REDIS_DB=${redisDatabase}` : "",
      redisPassword ? `REDIS_PWD=${redisPassword}` : "",
    ].filter(Boolean);
  }
  if (type === "postgres") {
    if (raw) return [`DATABASE_URL=${raw}`];
    if (!host) return [];
    return [`DATABASE_URL=postgres://${user}:@${host}:${port || "5432"}/${database}`];
  }
  if (type === "etcd") {
    const endpoint = raw || (host ? `${host}${port ? `:${port}` : ""}` : "");
    return endpoint ? [`ETCD_ENDPOINTS=${endpoint}`] : [];
  }
  if (type === "elasticsearch") {
    if (raw) return [`ELASTICSEARCH_HOSTS=${raw}`];
    if (!host) return [];
    return [`ELASTICSEARCH_HOSTS=http://${host}:${port || "9200"}`];
  }
  if (type === "kafka") {
    const bootstrap = host ? joinHostPort(host, port || "9092") : raw;
    if (!bootstrap) return [];
    return [
      `BOOTSTRAP_SERVERS=${bootstrap}`,
      user && password ? `KAFKA_API_KEY=${user}` : "",
      user && password ? `KAFKA_API_SECRET=${password}` : "",
      user && password ? "KAFKA_SECURITY_PROTOCOL=sasl_plaintext" : "",
      user && password ? "KAFKA_SASL_MECHANISM=PLAIN" : "",
    ].filter(Boolean);
  }
  if (type === "nacos") {
    const addr = host ? joinHostPort(host, port || "8848") : raw;
    return addr ? [`NACOS_ADDR=${addr}`] : [];
  }
  return [];
}

function argsFromDSN(type: string, dsn: Record<string, string>): string[] {
  void dsn;
  if (type !== "nacos") return [];
  return [];
}

// ---- MCPFormModal ----
export function MCPFormModal({
  editItem,
  prefill,
  projectId = "",
  containerOptions = [],
  containerOptionsLoading = false,
  onClose,
  onSaved,
  onTested,
}: {
  editItem?: MCPConnectionStatus | null;
  prefill?: MCPPrefill | null;
  projectId?: string;
  containerOptions?: MCPContainerBindingOption[];
  containerOptionsLoading?: boolean;
  onClose: () => void;
  onSaved?: (config: MCPConnectionConfig) => void;
  onTested?: () => void;
}) {
  const queryClient = useQueryClient();
  const [editing, setEditing] = React.useState<MCPConnectionStatus | null>(null);
  const [isNew, setIsNew] = React.useState(true);
  const [saving, setSaving] = React.useState(false);
  const [saveError, setSaveError] = React.useState("");
  const [testResult, setTestResult] = React.useState("");
  const [testing, setTesting] = React.useState(false);
  const [creds, setCreds] = React.useState<Credentials>(emptyCreds());
  const [bindingQuery, setBindingQuery] = React.useState("");
  const [bindingOpen, setBindingOpen] = React.useState(false);
  const [bindingDSNLoading, setBindingDSNLoading] = React.useState(false);
  const bindingInputRef = React.useRef<HTMLInputElement>(null);
  const bindingDSNRequestRef = React.useRef(0);

  // Track meaningful prefill identity to avoid re-init on every render.
  const prefillKey = prefill ? `${prefill.containerId || ""}:${prefill.nodeletId || ""}:${prefill.name || ""}` : "";
  const selectedBindingKey = editing?.nodeletId && editing.containerId
    ? `${editing.nodeletId}/${editing.containerId}`
    : "";
  const selectedBindingLabel = React.useMemo(() => {
    if (!editing?.nodeletId || !editing.containerId) return "";
    const option = containerOptions.find((item) =>
      item.nodeletId === editing.nodeletId && item.containerId === editing.containerId
    );
    return option
      ? bindingOptionLabel(option)
      : `${editing.containerId.slice(0, 12)} · ${editing.nodeletId}`;
  }, [editing?.nodeletId, editing?.containerId, containerOptions]);
  const filteredBindingOptions = React.useMemo(() => {
    const q = bindingQuery.trim().toLowerCase();
    if (!q) return containerOptions;
    return containerOptions.filter((option) => bindingOptionSearchText(option).includes(q));
  }, [bindingQuery, containerOptions]);

  // 初始化：editItem 优先（编辑模式），否则 prefill 或空白（新建模式）
  React.useEffect(() => {
    if (editItem) {
      setIsNew(false);
      const parsedCreds = parseCredentials(editItem.type, editItem.env, editItem.args);
      setEditing({
        ...editItem,
        env: stripGeneratedEnv(editItem.type, editItem.env, parsedCreds),
        args: stripGeneratedArgs(editItem.type, editItem.args),
      });
      setCreds(parsedCreds);
      setBindingQuery("");
    } else {
      setIsNew(true);
      const form = emptyForm(prefill?.type || "mysql");
      if (prefill) {
        form.name = prefill.name;
        if (!typeDefaults[form.type] && prefill.env.length > 0) {
          form.env = prefill.env;
        }
        form.containerId = prefill.containerId;
        form.nodeletId = prefill.nodeletId;
        const c = emptyCreds();
        if (prefill.host) c.host = prefill.host;
        if (prefill.port) c.port = String(prefill.port);
        if (prefill.user) c.user = prefill.user;
        if (prefill.database) c.database = prefill.database;
        setCreds(c);
      } else {
        // 从类型默认环境变量预填连接参数（主机、端口等）
        setCreds(parseCredentials(form.type, form.env, form.args));
      }
      setEditing(form);
      setBindingQuery("");
    }
    setTestResult("");
    setSaveError("");
  }, [editItem, prefillKey]);

  // 表单关键字段变化时清除旧的测试结果，避免展示过期数据。
  React.useEffect(() => {
    setTestResult("");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    editing?.type,
    editing?.transport,
    editing?.command,
    editing?.url,
    creds,
  ]);

  function closeForm() {
    if (saving) return;
    onClose();
  }

  function updateCreds(partial: Partial<Credentials>) {
    setCreds((prev) => ({ ...prev, ...partial }));
  }

  function refreshMCPStatus() {
    queryClient.invalidateQueries({ queryKey: queryKeys.mcp.all });
    if (projectId) {
      queryClient.invalidateQueries({ queryKey: queryKeys.mcp.byProject(projectId) });
    }
    queryClient.invalidateQueries({ queryKey: queryKeys.tools.all });
    onTested?.();
  }

  function openBindingSearch() {
    setBindingOpen(true);
  }

  function activateBindingSearch() {
    if (containerOptionsLoading || bindingDSNLoading) return;
    setBindingOpen(true);
    bindingInputRef.current?.focus();
  }

  function updateBindingQuery(value: string) {
    setBindingQuery(value);
    setBindingOpen(true);
    if (selectedBindingKey && value.trim()) {
      bindingDSNRequestRef.current += 1;
      setBindingDSNLoading(false);
      setEditing((prev) => prev ? { ...prev, nodeletId: "", containerId: "" } : prev);
    }
  }

  function clearBinding() {
    bindingDSNRequestRef.current += 1;
    setBindingDSNLoading(false);
    setBindingQuery("");
    setBindingOpen(false);
    setEditing((prev) => prev ? { ...prev, nodeletId: "", containerId: "" } : prev);
  }

  function applyBindingPrefill(option: MCPContainerBindingOption, config?: DSNConfig) {
    setEditing((prev) => {
      if (!prev) return prev;
      const nextType = supportedMCPType(option.serviceType, prev.type);
      const defaults = typeDefaults[nextType] || typeDefaults.other;
      const dsn = dsnRecord(config);
      const env = envFromDSN(nextType, dsn);
      const args = argsFromDSN(nextType, dsn);
      setCreds(parseCredentials(nextType, env, args));
      return {
        ...prev,
        name: prev.name || option.containerName,
        type: nextType,
        command: defaults.command,
        args: [...defaults.args],
        env: [],
        nodeletId: option.nodeletId,
        containerId: option.containerId,
      };
    });
  }

  async function selectBinding(option: MCPContainerBindingOption) {
    setBindingQuery("");
    setBindingOpen(false);
    setSaveError("");
    setEditing((prev) => prev ? { ...prev, nodeletId: option.nodeletId, containerId: option.containerId } : prev);

    const requestId = ++bindingDSNRequestRef.current;
    if (!projectId) {
      applyBindingPrefill(option);
      return;
    }

    setBindingDSNLoading(true);
    try {
      const config = await apiRequest<DSNConfig>(
        serverPaths(projectId, option.nodeletId).containerDSN(option.containerId),
      );
      if (bindingDSNRequestRef.current !== requestId) return;
      applyBindingPrefill(option, config);
    } catch (err) {
      if (bindingDSNRequestRef.current !== requestId) return;
      applyBindingPrefill(option);
      setSaveError(getErrorMessage(err, "读取容器 DSN 失败"));
    } finally {
      if (bindingDSNRequestRef.current === requestId) setBindingDSNLoading(false);
    }
  }

  async function handleSave() {
    if (!editing || saving) return;
    if (bindingQuery.trim() && !selectedBindingKey) {
      setSaveError("请从列表中选择绑定容器，或清空绑定");
      return;
    }
    const credentialError = validateCredentials(editing.type, creds);
    if (credentialError) {
      setSaveError(credentialError);
      return;
    }
    setSaving(true);
    setSaveError("");
    const body = formToConfig(editing, creds);

    if (isNew) {
      // 从名称生成 ID，添加时间戳后缀保证唯一性
      const base = editing.name.toLowerCase().replace(/\s+/g, "-").replace(/[^a-z0-9-]/g, "") || "mcp";
      body.id = `${base}-${Date.now()}`;
    }

    const url = isNew
      ? mcpConnectionPaths.list
      : mcpConnectionPaths.detail(body.id);
    const method = isNew ? "POST" : "PUT";

    try {
      await apiRequest(url, {
        method,
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      onSaved?.(body);
      onClose();
    } catch (err) {
      setSaveError(getErrorMessage(err, "保存失败"));
    } finally {
      setSaving(false);
    }
  }

  async function handleTest() {
    if (!editing) return;
    const credentialError = validateCredentials(editing.type, creds);
    if (credentialError) {
      setTestResult(credentialError);
      return;
    }
    setTesting(true);
    setTestResult("");
    try {
      const data = await apiRequest<{ status: string; error?: string }>(mcpConnectionPaths.test, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(formToConfig(editing, creds)),
      });
      if (data.status === "ok") {
        setTestResult("连接测试成功 ✓");
      } else if (data.status === "transport_error") {
        setTestResult(`无法连接: ${data.error || "未知错误"}`);
      } else {
        setTestResult(`连接失败: ${data.error || "未知错误"}`);
      }
    } catch (err) {
      setTestResult(getErrorMessage(err, "测试请求失败"));
    } finally {
      setTesting(false);
      refreshMCPStatus();
    }
  }

  const showCredentials = typesWithCredentials.has(editing?.type || "");
  const credFields = typeCredentialFields[editing?.type || ""] || [];
  const args = editing?.args || [];

  function updateArgs(value: string) {
    setEditing((prev) => prev ? { ...prev, args: parseArgLines(value) } : prev);
  }

  return (
    <Modal
      title={isNew ? "新增 MCP 连接" : "编辑 MCP 连接"}
      onClose={closeForm}
      maxWidth="560px"
      footer={
        <>
          <Button variant="ghost" onClick={handleTest} disabled={testing || bindingDSNLoading}>
            {testing ? "测试中..." : "测试连接"}
          </Button>
          <Button onClick={handleSave} disabled={!editing?.name.trim() || saving || bindingDSNLoading}>
            {saving ? "保存中..." : "保存"}
          </Button>
        </>
      }
    >
      <label htmlFor="mcp-name">名称</label>
      <FormInput
        id="mcp-name"
        value={editing?.name || ""}
        onChange={(e) => setEditing((prev) => prev ? { ...prev, name: e.target.value } : prev)}
      />

      <label htmlFor="mcp-type">类型</label>
      <select
        id="mcp-type"
        value={editing?.type || "mysql"}
        onChange={(e) => {
          const newType = e.target.value;
          const defaults = typeDefaults[newType] || typeDefaults.other;
          setEditing((prev) => {
            if (!prev) return prev;
            const newCreds = parseCredentials(newType, defaults.env, defaults.args);
            setCreds(newCreds);
            return {
              ...prev,
              type: newType,
              command: defaults.command,
              args: [...defaults.args],
              env: [],
            };
          });
        }}
      >
        <option value="mysql">MySQL</option>
        <option value="redis">Redis</option>
        <option value="postgres">PostgreSQL</option>
        <option value="etcd">Etcd</option>
        <option value="elasticsearch">Elasticsearch</option>
        <option value="kafka">Kafka</option>
        <option value="nacos">Nacos</option>
        <option value="other">其他</option>
      </select>

      <label htmlFor="mcp-container-binding">绑定容器</label>
      <div className="mcp-binding-control">
        <div className="mcp-binding-row">
          <div
            className={`mcp-binding-input-shell ${bindingOpen ? "focused" : ""}`}
            onMouseDown={(e) => {
              if (e.target !== bindingInputRef.current) e.preventDefault();
              activateBindingSearch();
            }}
          >
            {selectedBindingKey && !bindingQuery && (
              <span className="mcp-binding-chip">
                <span className="mcp-binding-chip-text">{selectedBindingLabel}</span>
              </span>
            )}
            <input
              ref={bindingInputRef}
              id="mcp-container-binding"
              className="mcp-binding-input"
              value={bindingQuery}
              onChange={(e) => updateBindingQuery(e.target.value)}
              onFocus={openBindingSearch}
              onBlur={() => window.setTimeout(() => setBindingOpen(false), 120)}
              onKeyDown={(e) => {
                if (e.key === "Escape") setBindingOpen(false);
                if ((e.key === "Backspace" || e.key === "Delete") && selectedBindingKey && !bindingQuery) {
                  clearBinding();
                }
              }}
              disabled={containerOptionsLoading || bindingDSNLoading}
              autoComplete="off"
            />
          </div>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={clearBinding}
            disabled={bindingDSNLoading || (!selectedBindingKey && !bindingQuery)}
          >
            清除
          </Button>
        </div>
        {bindingOpen && (
          <div className="mcp-binding-menu">
            {containerOptionsLoading ? (
              <div className="mcp-binding-empty">读取容器中...</div>
            ) : filteredBindingOptions.length === 0 ? (
              <div className="mcp-binding-empty">没有匹配容器</div>
            ) : (
              filteredBindingOptions.map((option) => {
                const selected = editing?.nodeletId === option.nodeletId &&
                  editing?.containerId === option.containerId;
                const metaParts = [
                  option.containerName,
                  bindingServiceText(option),
                  option.containerId.slice(0, 12),
                ].filter(Boolean);
                return (
                  <button
                    type="button"
                    key={`${option.nodeletId}/${option.containerId}`}
                    className={`mcp-binding-option ${selected ? "selected" : ""}`}
                    onMouseDown={(e) => e.preventDefault()}
                    onClick={() => selectBinding(option)}
                  >
                    <span className="mcp-binding-name">{option.nodeletName}</span>
                    <span className="mcp-binding-meta">{metaParts.join(" · ")}</span>
                  </button>
                );
              })
            )}
          </div>
        )}
      </div>
      <div className="field-hint">
        {bindingDSNLoading ? "正在读取容器 DSN..." : "选择容器后会自动填充 MCP 连接参数。"}
      </div>

      <label htmlFor="mcp-transport">传输方式</label>
      <select
        id="mcp-transport"
        value={editing?.transport || "stdio"}
        onChange={(e) => setEditing((prev) => prev ? { ...prev, transport: e.target.value } : prev)}
      >
        <option value="stdio">stdio（本地子进程）</option>
        <option value="sse">SSE（远程服务）</option>
      </select>

      {(editing?.transport || "stdio") === "sse" ? (
        <>
          <label htmlFor="mcp-url">SSE 地址</label>
          <FormInput
            id="mcp-url"
            value={editing?.url || ""}
            onChange={(e) => setEditing((prev) => prev ? { ...prev, url: e.target.value } : prev)}
          />
        </>
      ) : (
        <>
          <label htmlFor="mcp-command">命令路径</label>
          <FormInput
            id="mcp-command"
            value={editing?.command || ""}
            onChange={(e) => setEditing((prev) => prev ? { ...prev, command: e.target.value } : prev)}
          />

          <label htmlFor="mcp-args">额外参数（每行一个）</label>
          <FormInput
            id="mcp-args"
            multiline
            monospace
            className="mcp-raw-textarea"
            value={args.join("\n")}
            onChange={(e) => updateArgs(e.target.value)}
          />
        </>
      )}

      {/* 连接参数 —— 按类型展示不同字段 */}
      {showCredentials && credFields.length > 0 && (
        <fieldset className="creds-fieldset">
          <legend>连接参数</legend>
          <div className="creds-grid">
            {credFields.map((f) => (
              <React.Fragment key={f.key}>
                <label htmlFor={`mcp-creds-${f.key}`}>{f.label}</label>
                {f.options ? (
                  <select
                    id={`mcp-creds-${f.key}`}
                    value={creds[f.key]}
                    onChange={(e) => updateCreds({ [f.key]: e.target.value })}
                  >
                    {f.options.map((option) => (
                      <option key={option.value} value={option.value}>
                        {option.label}
                      </option>
                    ))}
                  </select>
                ) : f.isPassword ? (
                  <input
                    id={`mcp-creds-${f.key}`}
                    className="form-input"
                    type="password"
                    value={creds[f.key]}
                    onChange={(e) => updateCreds({ [f.key]: e.target.value })}
                    autoComplete="new-password"
                  />
                ) : (
                  <FormInput
                    id={`mcp-creds-${f.key}`}
                    value={creds[f.key]}
                    onChange={(e) => updateCreds({ [f.key]: e.target.value })}
                  />
                )}
              </React.Fragment>
            ))}
          </div>
        </fieldset>
      )}

      <label htmlFor="mcp-env">额外环境变量（KEY=VALUE，每行一个）</label>
      <FormInput
        id="mcp-env"
        multiline
        monospace
        className="mcp-raw-textarea"
        value={editing?.env.join("\n") || ""}
        onChange={(e) => setEditing((prev) => prev ? { ...prev, env: e.target.value.split("\n").filter(Boolean) } : prev)}
        spellCheck={false}
      />

      <label className="checkbox-label">
        <input
          type="checkbox"
          checked={editing?.enabled ?? true}
          onChange={(e) => setEditing((prev) => prev ? { ...prev, enabled: e.target.checked } : prev)}
        />
        <span>启用</span>
      </label>

      {saveError && <div className="error-banner">{saveError}</div>}

      {testResult && (
        <div className={testResult.includes("成功") ? "success-banner" : "error-banner"}>
          {testResult}
        </div>
      )}
    </Modal>
  );
}
