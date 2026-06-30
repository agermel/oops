import React from "react";
import type { MCPConnectionConfig, MCPConnectionStatus, MCPPrefill } from "../types";
import { apiRequest, getErrorMessage } from "../lib/api";
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
    command: defaults.command,
    args: [...defaults.args],
    env: [...defaults.env],
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
    command: form.command,
    args: form.args,
    env: form.env,
    enabled: form.enabled,
    containerId: form.containerId,
    nodeletId: form.nodeletId,
  };
}

// ---- MCPFormModal ----
export function MCPFormModal({
  editItem,
  prefill,
  onClose,
  onSaved,
}: {
  editItem?: MCPConnectionStatus | null;
  prefill?: MCPPrefill | null;
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

  // 初始化：editItem 优先（编辑模式），否则 prefill 或空白（新建模式）
  React.useEffect(() => {
    if (editItem) {
      setIsNew(false);
      setEditing({ ...editItem });
      setCreds(parseCredentials(editItem.type, editItem.env));
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
    }
    setTestResult("");
    setSaveError("");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  function closeForm() {
    if (saving) return;
    onClose();
  }

  // 连接参数变化时，自动同步到 env
  function updateCreds(partial: Partial<Credentials>) {
    setCreds((prev) => {
      const next = { ...prev, ...partial };
      setEditing((form) => {
        if (!form) return form;
        const generated = credentialsToEnv(form.type, next);
        if (generated.length === 0) return form;
        const genKeys = new Set(generated.map((e) => e.split("=")[0]));
        const kept = form.env.filter((e) => !genKeys.has(e.split("=")[0]));
        return { ...form, env: [...generated, ...kept] };
      });
      return next;
    });
  }

  async function handleSave() {
    if (!editing || saving) return;
    setSaving(true);
    setSaveError("");
    const body = formToConfig(editing);

    if (isNew) {
      // 从名称生成 ID，添加时间戳后缀保证唯一性
      const base = editing.name.toLowerCase().replace(/\s+/g, "-").replace(/[^a-z0-9-]/g, "") || "mcp";
      body.id = `${base}-${Date.now()}`;
    }

    const url = isNew
      ? "/api/mcp/connections"
      : `/api/mcp/connections/${encodeURIComponent(body.id)}`;
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
      const data = await apiRequest<{ status: string; error?: string }>("/api/mcp/connections/test", {
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
          <Button variant="ghost" onClick={handleTest} disabled={testing}>
            {testing ? "测试中..." : "测试连接"}
          </Button>
          <Button onClick={handleSave} disabled={!editing?.name.trim() || saving}>
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
