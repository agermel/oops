import React from "react";
import type {
  MCPConnectionConfig,
  MCPConnectionStatus,
  MCPPrefill,
  MCPContainerBindingOption,
  DSNConfig,
} from "../types";
import { serviceLabel } from "../types";
import { apiRequest, getErrorMessage } from "../lib/api";
import { mcpConnectionPaths, serverPaths } from "../lib/paths";
import { Modal } from "./Modal";
import { Button } from "./ui/Button";
import { FormInput } from "./ui/FormInput";

// ---- 类型默认值 ----
const typeDefaults: Record<string, { command: string; args: string[]; env: string[] }> = {
  mysql: {
    command: "./mcp-servers/mysql/mysql-mcp-server",
    args: ["--silent"],
    env: ["MYSQL_DSN=user:pass@tcp(host:3306)/db?charset=utf8mb4"],
  },
  redis: {
    command: "./mcp-servers/redis/redis-mcp-server",
    args: [],
    env: ["REDIS_HOST=127.0.0.1", "REDIS_PORT=6379", "REDIS_DB=0", "REDIS_PWD="],
  },
  etcd: {
    command: "./mcp-servers/etcd/etcd-mcp-server",
    args: [],
    env: ["ETCD_ENDPOINTS=127.0.0.1:2379"],
  },
  postgres: {
    command: "uvx",
    args: ["--from", "mcp-server-postgres@latest", "mcp-server-postgres"],
    env: ["DATABASE_URL=postgres://user:pass@host:5432/db"],
  },
  elasticsearch: {
    command: "./mcp-servers/elasticsearch/elasticsearch-mcp-server",
    args: [],
    env: ["ELASTICSEARCH_URL=http://127.0.0.1:9200"],
  },
  other: {
    command: "",
    args: [],
    env: [],
  },
};

// 哪些类型显示连接参数字段
const typesWithCredentials = new Set(["mysql", "redis", "postgres", "etcd", "elasticsearch"]);

// ---- 连接参数 ----
type Credentials = {
  host: string;
  port: string;
  user: string;
  password: string;
  database: string;
};

function emptyCreds(): Credentials {
  return { host: "", port: "", user: "", password: "", database: "" };
}

// 每种类型展示的连接参数字段（按序）。
interface CredentialField {
  key: keyof Credentials;
  label: string;
  placeholder: string;
  /** 设为 true 时使用 password 输入框 */
  isPassword?: boolean;
}

const typeCredentialFields: Record<string, CredentialField[]> = {
  mysql: [
    { key: "host", label: "主机", placeholder: "host" },
    { key: "port", label: "端口", placeholder: "3306" },
    { key: "user", label: "用户", placeholder: "root" },
    { key: "password", label: "密码", placeholder: "输入密码", isPassword: true },
    { key: "database", label: "数据库", placeholder: "mysql" },
  ],
  redis: [
    { key: "host", label: "主机", placeholder: "127.0.0.1" },
    { key: "port", label: "端口", placeholder: "6379" },
    { key: "password", label: "密码", placeholder: "输入密码", isPassword: true },
    { key: "database", label: "DB 编号", placeholder: "0" },
  ],
  postgres: [
    { key: "host", label: "主机", placeholder: "host" },
    { key: "port", label: "端口", placeholder: "5432" },
    { key: "user", label: "用户", placeholder: "postgres" },
    { key: "password", label: "密码", placeholder: "输入密码", isPassword: true },
    { key: "database", label: "数据库", placeholder: "postgres" },
  ],
  etcd: [
    { key: "host", label: "端点", placeholder: "127.0.0.1:2379" },
    { key: "user", label: "用户", placeholder: "(可选)" },
    { key: "password", label: "密码", placeholder: "输入密码", isPassword: true },
  ],
  elasticsearch: [
    { key: "host", label: "地址", placeholder: "127.0.0.1" },
    { key: "port", label: "端口", placeholder: "9200" },
    { key: "user", label: "用户", placeholder: "elastic" },
    { key: "password", label: "密码", placeholder: "输入密码", isPassword: true },
  ],
};

// 从环境变量列表反解连接参数
function parseCredentials(type: string, env: string[]): Credentials {
  const creds = emptyCreds();
  if (type === "mysql") {
    const dsn = env.find((e) => e.startsWith("MYSQL_DSN="))?.slice("MYSQL_DSN=".length) || "";
    const m = dsn.match(/^([^:]*):([^@]*)@tcp\(([^:]*):(\d*)\)\/(.*?)(\?.*)?$/);
    if (m) {
      creds.user = m[1] || "";
      creds.password = m[2] || "";
      creds.host = m[3] || "";
      creds.port = m[4] || "";
      creds.database = m[5] || "";
    }
  } else if (type === "redis") {
    creds.host = env.find((e) => e.startsWith("REDIS_HOST="))?.slice("REDIS_HOST=".length) || "";
    creds.port = env.find((e) => e.startsWith("REDIS_PORT="))?.slice("REDIS_PORT=".length) || "";
    creds.password = env.find((e) => e.startsWith("REDIS_PWD="))?.slice("REDIS_PWD=".length) || "";
    creds.database = env.find((e) => e.startsWith("REDIS_DB="))?.slice("REDIS_DB=".length) || "";
  } else if (type === "postgres") {
    const dsn = env.find((e) => e.startsWith("DATABASE_URL="))?.slice("DATABASE_URL=".length) || "";
    const m = dsn.match(/^postgres:\/\/([^:]*):([^@]*)@([^:]*):(\d*)\/(.*)$/);
    if (m) {
      creds.user = m[1] || "";
      creds.password = m[2] || "";
      creds.host = m[3] || "";
      creds.port = m[4] || "";
      creds.database = m[5] || "";
    }
  } else if (type === "etcd") {
    creds.host = env.find((e) => e.startsWith("ETCD_ENDPOINTS="))?.slice("ETCD_ENDPOINTS=".length) || "";
    creds.user = env.find((e) => e.startsWith("ETCD_USERNAME="))?.slice("ETCD_USERNAME=".length) || "";
    creds.password = env.find((e) => e.startsWith("ETCD_PASSWORD="))?.slice("ETCD_PASSWORD=".length) || "";
  } else if (type === "elasticsearch") {
    const esUrl = env.find((e) => e.startsWith("ELASTICSEARCH_URL="))?.slice("ELASTICSEARCH_URL=".length) || "";
    const m = esUrl.match(/^(?:https?:\/\/)(?:([^:]+):([^@]+)@)?([^:/]+)(?::(\d+))?/);
    if (m) {
      creds.user = m[1] || env.find((e) => e.startsWith("ELASTICSEARCH_USERNAME="))?.slice("ELASTICSEARCH_USERNAME=".length) || "";
      creds.password = m[2] || env.find((e) => e.startsWith("ELASTICSEARCH_PASSWORD="))?.slice("ELASTICSEARCH_PASSWORD=".length) || "";
      creds.host = m[3] || "";
      creds.port = m[4] || "9200";
    } else {
      creds.host = esUrl || "";
      creds.port = "9200";
    }
  }
  return creds;
}

// 从连接参数生成环境变量
function credentialsToEnv(type: string, creds: Credentials): string[] {
  if (type === "mysql") {
    const pass = creds.password ? `:${creds.password}` : "";
    const dsn = `${creds.user}${pass}@tcp(${creds.host}:${creds.port})/${creds.database}?charset=utf8mb4`;
    if (!creds.user && !creds.host) return [];
    return [`MYSQL_DSN=${dsn}`];
  }
  if (type === "redis") {
    const env: string[] = [];
    if (creds.host) env.push(`REDIS_HOST=${creds.host}`);
    if (creds.port) env.push(`REDIS_PORT=${creds.port}`);
    if (creds.database) env.push(`REDIS_DB=${creds.database}`);
    if (creds.password) env.push(`REDIS_PWD=${creds.password}`);
    else env.push("REDIS_PWD=");
    return env;
  }
  if (type === "postgres") {
    const pass = creds.password ? `:${creds.password}` : "";
    const dsn = `postgres://${creds.user}${pass}@${creds.host}:${creds.port}/${creds.database}`;
    if (!creds.user && !creds.host) return [];
    return [`DATABASE_URL=${dsn}`];
  }
  if (type === "etcd") {
    const env: string[] = [];
    if (creds.host) env.push(`ETCD_ENDPOINTS=${creds.host}`);
    if (creds.user) env.push(`ETCD_USERNAME=${creds.user}`);
    if (creds.password) env.push(`ETCD_PASSWORD=${creds.password}`);
    return env;
  }
  if (type === "elasticsearch") {
    const env: string[] = [];
    if (creds.host) {
      const port = creds.port !== "9200" ? `:${creds.port}` : ":9200";
      env.push(`ELASTICSEARCH_URL=http://${creds.host}${port}`);
    }
    if (creds.user) env.push(`ELASTICSEARCH_USERNAME=${creds.user}`);
    if (creds.password) env.push(`ELASTICSEARCH_PASSWORD=${creds.password}`);
    return env;
  }
  return [];
}

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

function formToConfig(form: MCPConnectionStatus): MCPConnectionConfig {
  return {
    id: form.id,
    name: form.name,
    type: form.type,
    transport: form.transport,
    command: form.command,
    args: form.args,
    env: form.env,
    url: form.url,
    enabled: form.enabled,
    containerId: form.containerId,
    nodeletId: form.nodeletId,
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
    let redisDatabase = database;
    let redisPassword = password;
    if (raw) {
      try {
        const url = new URL(raw);
        redisHost ||= url.hostname;
        redisPort ||= url.port || "6379";
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
    if (!redisHost && !redisPort && !redisDatabase && !redisPassword) return [];
    return [
      redisHost ? `REDIS_HOST=${redisHost}` : "",
      redisPort ? `REDIS_PORT=${redisPort}` : "",
      `REDIS_DB=${redisDatabase || "0"}`,
      redisPassword ? `REDIS_PWD=${redisPassword}` : "REDIS_PWD=",
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
    if (raw) return [`ELASTICSEARCH_URL=${raw}`];
    if (!host) return [];
    return [`ELASTICSEARCH_URL=http://${host}:${port || "9200"}`];
  }
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
}: {
  editItem?: MCPConnectionStatus | null;
  prefill?: MCPPrefill | null;
  projectId?: string;
  containerOptions?: MCPContainerBindingOption[];
  containerOptionsLoading?: boolean;
  onClose: () => void;
  onSaved?: (config: MCPConnectionConfig) => void;
}) {
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
      setEditing({ ...editItem });
      setCreds(parseCredentials(editItem.type, editItem.env));
      setBindingQuery("");
    } else {
      setIsNew(true);
      const form = emptyForm(prefill?.type || "mysql");
      if (prefill) {
        form.name = prefill.name;
        form.env = prefill.env.length > 0 ? prefill.env : form.env;
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
        setCreds(parseCredentials(form.type, form.env));
      }
      setEditing(form);
      setBindingQuery("");
    }
    setTestResult("");
    setSaveError("");
  }, [editItem, prefillKey]);

  function closeForm() {
    if (saving) return;
    onClose();
  }

  // 连接参数变化时，自动同步到 env
  function updateCreds(partial: Partial<Credentials>) {
    setCreds((prev) => ({ ...prev, ...partial }));
    // Sync editing.env based on new creds values (React 18 batches both setStates).
    setEditing((form) => {
      if (!form) return form;
      // Read latest creds via functional updater that merges the partial
      const nextCreds = { ...creds, ...partial };
      const generated = credentialsToEnv(form.type, nextCreds);
      if (generated.length === 0) return form;
      const genKeys = new Set(generated.map((e) => e.split("=")[0]));
      const kept = form.env.filter((e) => !genKeys.has(e.split("=")[0]));
      return { ...form, env: [...generated, ...kept] };
    });
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
      const env = envFromDSN(nextType, dsnRecord(config));
      const nextEnv = env.length > 0 ? env : [...defaults.env];
      setCreds(parseCredentials(nextType, nextEnv));
      return {
        ...prev,
        name: prev.name || option.containerName,
        type: nextType,
        command: defaults.command,
        args: [...defaults.args],
        env: nextEnv,
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
    setSaving(true);
    setSaveError("");
    const body = formToConfig(editing);

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
    setTesting(true);
    setTestResult("");
    try {
      const data = await apiRequest<{ status: string; error?: string }>(mcpConnectionPaths.test, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(formToConfig(editing)),
      });
      if (data.status === "ok") {
        setTestResult("连接测试成功 ✓");
      } else {
        setTestResult(`连接失败: ${data.error || "未知错误"}`);
      }
    } catch (err) {
      setTestResult(getErrorMessage(err, "测试请求失败"));
    } finally {
      setTesting(false);
    }
  }

  const showCredentials = typesWithCredentials.has(editing?.type || "");
  const credFields = typeCredentialFields[editing?.type || ""] || [];

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
        placeholder="例如: 生产环境 MySQL"
      />

      <label htmlFor="mcp-type">类型</label>
      <select
        id="mcp-type"
        value={editing?.type || "mysql"}
        onChange={(e) => {
          const newType = e.target.value;
          const defaults = typeDefaults[newType] || typeDefaults.other;
          // 保留用户手动添加的环境变量中不属于新类型默认生成器的键
          setEditing((prev) => {
            if (!prev) return prev;
            const prevTypeDefaults = typeDefaults[prev.type] || typeDefaults.other;
            const prevDefaultKeys = new Set(prevTypeDefaults.env.map((ev) => ev.split("=")[0]));
            // 用户手动添加的 env（不在旧类型默认键中）
            const userEnv = prev.env.filter((ev) => !prevDefaultKeys.has(ev.split("=")[0]));
            const newCreds = parseCredentials(newType, defaults.env);
            setCreds(newCreds);
            return {
              ...prev,
              type: newType,
              command: defaults.command,
              args: [...defaults.args],
              env: [...defaults.env, ...userEnv],
            };
          });
        }}
      >
        <option value="mysql">MySQL</option>
        <option value="redis">Redis</option>
        <option value="postgres">PostgreSQL</option>
        <option value="etcd">Etcd</option>
        <option value="elasticsearch">Elasticsearch</option>
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
              placeholder={
                selectedBindingKey
                  ? ""
                  : containerOptionsLoading
                    ? "读取容器中..."
                    : "搜索容器名、服务器或类型"
              }
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
            placeholder="http://10.0.0.1:19900/sse"
          />
        </>
      ) : (
        <>
          <label htmlFor="mcp-command">命令路径</label>
          <FormInput
            id="mcp-command"
            value={editing?.command || ""}
            onChange={(e) => setEditing((prev) => prev ? { ...prev, command: e.target.value } : prev)}
            placeholder="mysql-mcp-server"
          />

          <label htmlFor="mcp-args">参数（每行一个）</label>
          <FormInput
            id="mcp-args"
            multiline
            monospace
            value={editing?.args.join("\n") || ""}
            onChange={(e) => setEditing((prev) => prev ? { ...prev, args: e.target.value.split("\n").filter(Boolean) } : prev)}
            placeholder="--read-only"
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
                {f.isPassword ? (
                  <input
                    id={`mcp-creds-${f.key}`}
                    className="form-input"
                    type="password"
                    value={creds[f.key]}
                    onChange={(e) => updateCreds({ [f.key]: e.target.value })}
                    placeholder={f.placeholder}
                    autoComplete="new-password"
                  />
                ) : (
                  <FormInput
                    id={`mcp-creds-${f.key}`}
                    value={creds[f.key]}
                    onChange={(e) => updateCreds({ [f.key]: e.target.value })}
                    placeholder={f.placeholder}
                  />
                )}
              </React.Fragment>
            ))}
          </div>
        </fieldset>
      )}

      <label htmlFor="mcp-env">环境变量（KEY=VALUE，每行一个）</label>
      <FormInput
        id="mcp-env"
        multiline
        monospace
        value={editing?.env.join("\n") || ""}
        onChange={(e) => setEditing((prev) => prev ? { ...prev, env: e.target.value.split("\n").filter(Boolean) } : prev)}
        placeholder="MYSQL_DSN=user:pass@tcp(host:3306)/db?charset=utf8mb4"
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
