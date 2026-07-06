import type { MCPConnectionConfig, MCPConnectionStatus } from "../types";

export type MCPCredentials = {
  host: string;
  port: string;
  user: string;
  password: string;
  database: string;
  securityProtocol: string;
  saslMechanism: string;
};

export function emptyMCPCredentials(): MCPCredentials {
  return {
    host: "",
    port: "",
    user: "",
    password: "",
    database: "",
    securityProtocol: "",
    saslMechanism: "",
  };
}

function applyCredentialDefaults(type: string, creds: MCPCredentials): MCPCredentials {
  if (type !== "kafka") return creds;
  return {
    ...creds,
    securityProtocol: creds.securityProtocol || "sasl_plaintext",
    saslMechanism: creds.saslMechanism || "PLAIN",
  };
}

export function envKey(line: string): string {
  return line.split("=")[0]?.trim() || "";
}

function envValue(env: string[], key: string): string {
  const prefix = `${key}=`;
  return env.find((item) => item.startsWith(prefix))?.slice(prefix.length).trim() || "";
}

function argValue(args: string[], flag: string): string {
  const idx = args.indexOf(flag);
  if (idx < 0) return "";
  return args[idx + 1] || "";
}

export function splitHostPort(value: string): { host: string; port: string } {
  const first = value.split(",")[0]?.trim() || "";
  const match = first.match(/^(?:[a-zA-Z]+:\/\/)?([^:]+):(\d+)$/);
  if (!match || first !== value.trim()) return { host: value, port: "" };
  return { host: match[1] || "", port: match[2] || "" };
}

export function joinHostPort(host: string, port: string): string {
  if (!host) return "";
  if (!port || host.includes(":") || host.includes(",") || host.includes("://")) return host;
  return `${host}:${port}`;
}

export function parseMCPConnectionCredentials(type: string, env: string[], args: string[] = []): MCPCredentials {
  const creds = emptyMCPCredentials();
  if (type === "mysql") {
    const dsn = envValue(env, "MYSQL_DSN");
    const m = dsn.match(/^([^:]*):([^@]*)@tcp\(([^:]*):(\d*)\)\/(.*?)(\?.*)?$/);
    if (m) {
      creds.user = m[1] || "";
      creds.password = m[2] || "";
      creds.host = m[3] || "";
      creds.port = m[4] || "";
      creds.database = m[5] || "";
    }
  } else if (type === "redis") {
    creds.host = envValue(env, "REDIS_HOST");
    creds.port = envValue(env, "REDIS_PORT");
    creds.user = envValue(env, "REDIS_USERNAME");
    creds.password = envValue(env, "REDIS_PWD") || envValue(env, "REDIS_PASSWORD");
    creds.database = envValue(env, "REDIS_DB");
  } else if (type === "postgres") {
    const dsn = envValue(env, "DATABASE_URL");
    const m = dsn.match(/^postgres:\/\/([^:]*):([^@]*)@([^:]*):(\d*)\/(.*)$/);
    if (m) {
      creds.user = m[1] || "";
      creds.password = m[2] || "";
      creds.host = m[3] || "";
      creds.port = m[4] || "";
      creds.database = m[5] || "";
    }
  } else if (type === "etcd") {
    creds.host = envValue(env, "ETCD_ENDPOINTS");
    creds.user = envValue(env, "ETCD_USERNAME");
    creds.password = envValue(env, "ETCD_PASSWORD");
  } else if (type === "elasticsearch") {
    const esUrl = envValue(env, "ELASTICSEARCH_HOSTS") || envValue(env, "ELASTICSEARCH_URL");
    const m = esUrl.match(/^(?:https?:\/\/)(?:([^:]+):([^@]+)@)?([^:/]+)(?::(\d+))?/);
    if (m) {
      creds.user = m[1] || envValue(env, "ELASTICSEARCH_USERNAME");
      creds.password = m[2] || envValue(env, "ELASTICSEARCH_PASSWORD");
      creds.host = m[3] || "";
      creds.port = m[4] || "9200";
    } else {
      creds.host = esUrl || "";
      creds.port = "9200";
    }
  } else if (type === "kafka") {
    const bootstrap = envValue(env, "BOOTSTRAP_SERVERS") || envValue(env, "KAFKA_BOOTSTRAP_SERVERS");
    const parsed = splitHostPort(bootstrap);
    creds.host = parsed.host;
    creds.port = parsed.port;
    creds.user = envValue(env, "KAFKA_API_KEY");
    creds.password = envValue(env, "KAFKA_API_SECRET");
    creds.securityProtocol = envValue(env, "KAFKA_SECURITY_PROTOCOL");
    creds.saslMechanism = envValue(env, "KAFKA_SASL_MECHANISM") || envValue(env, "KAFKA_SASL_MECHANISMS");
  } else if (type === "nacos") {
    const parsed = splitHostPort(envValue(env, "NACOS_ADDR"));
    creds.host = parsed.host || argValue(args, "--host");
    creds.port = parsed.port || argValue(args, "--port");
    creds.user = envValue(env, "NACOS_USERNAME");
    creds.password = envValue(env, "NACOS_PASSWORD");
    creds.database = envValue(env, "NACOS_NAMESPACE");
  }
  return applyCredentialDefaults(type, creds);
}

export function mcpCredentialsToArgs(type: string, creds: MCPCredentials): string[] | null {
  void creds;
  if (type !== "nacos") return null;
  return [];
}

export function mcpCredentialsToEnv(type: string, creds: MCPCredentials): string[] {
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
    if (creds.user) env.push(`REDIS_USERNAME=${creds.user}`);
    if (creds.database) env.push(`REDIS_DB=${creds.database}`);
    if (creds.password) env.push(`REDIS_PWD=${creds.password}`);
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
      env.push(`ELASTICSEARCH_HOSTS=http://${creds.host}${port}`);
    }
    if (creds.user) env.push(`ELASTICSEARCH_USERNAME=${creds.user}`);
    if (creds.password) env.push(`ELASTICSEARCH_PASSWORD=${creds.password}`);
    return env;
  }
  if (type === "kafka") {
    const env: string[] = [];
    const bootstrap = joinHostPort(creds.host, creds.port);
    if (bootstrap) env.push(`BOOTSTRAP_SERVERS=${bootstrap}`);
    if (creds.user && creds.password) {
      env.push(`KAFKA_API_KEY=${creds.user}`);
      env.push(`KAFKA_API_SECRET=${creds.password}`);
    }
    if (creds.securityProtocol) env.push(`KAFKA_SECURITY_PROTOCOL=${creds.securityProtocol}`);
    if (creds.saslMechanism) env.push(`KAFKA_SASL_MECHANISM=${creds.saslMechanism}`);
    return env;
  }
  if (type === "nacos") {
    const env: string[] = [];
    const addr = joinHostPort(creds.host, creds.port);
    if (addr) env.push(`NACOS_ADDR=${addr}`);
    if (creds.user) env.push(`NACOS_USERNAME=${creds.user}`);
    if (creds.password) env.push(`NACOS_PASSWORD=${creds.password}`);
    if (creds.database) env.push(`NACOS_NAMESPACE=${creds.database}`);
    return env;
  }
  return [];
}

export function generatedMCPEnvKeys(type: string, generated: string[]): Set<string> {
  const keys = new Set(generated.map(envKey));
  if (generated.length === 0) return keys;
  if (type === "mysql") {
    keys.add("MYSQL_DSN");
  } else if (type === "redis") {
    keys.add("REDIS_HOST");
    keys.add("REDIS_PORT");
    keys.add("REDIS_USERNAME");
    keys.add("REDIS_DB");
    keys.add("REDIS_PWD");
    keys.add("REDIS_PASSWORD");
  } else if (type === "postgres") {
    keys.add("DATABASE_URL");
  } else if (type === "etcd") {
    keys.add("ETCD_ENDPOINTS");
    keys.add("ETCD_USERNAME");
    keys.add("ETCD_PASSWORD");
  } else if (type === "elasticsearch") {
    keys.add("ELASTICSEARCH_HOSTS");
    keys.add("ELASTICSEARCH_URL");
    keys.add("ELASTICSEARCH_USERNAME");
    keys.add("ELASTICSEARCH_PASSWORD");
  } else if (type === "kafka") {
    keys.add("BOOTSTRAP_SERVERS");
    keys.add("KAFKA_BOOTSTRAP_SERVERS");
    keys.add("KAFKA_API_KEY");
    keys.add("KAFKA_API_SECRET");
    keys.add("KAFKA_SECURITY_PROTOCOL");
    keys.add("KAFKA_SASL_MECHANISM");
    keys.add("KAFKA_SASL_MECHANISMS");
  } else if (type === "nacos") {
    keys.add("NACOS_ADDR");
    keys.add("NACOS_USERNAME");
    keys.add("NACOS_PASSWORD");
    keys.add("NACOS_NAMESPACE");
  }
  return keys;
}

export function stripGeneratedMCPEnv(type: string, env: string[], creds: MCPCredentials): string[] {
  const generated = mcpCredentialsToEnv(type, creds);
  if (generated.length === 0) return env;
  const generatedKeys = generatedMCPEnvKeys(type, generated);
  return env.filter((item) => !generatedKeys.has(envKey(item)));
}

export function stripGeneratedMCPArgs(type: string, args: string[]): string[] {
  if (type !== "nacos") return args;
  const generatedFlags = new Set(["--host", "--port", "--access_token"]);
  const kept: string[] = [];
  for (let i = 0; i < args.length; i += 1) {
    const arg = args[i];
    if (generatedFlags.has(arg)) {
      i += 1;
      continue;
    }
    kept.push(arg);
  }
  return kept;
}

export function buildMCPConnectionConfig(form: MCPConnectionStatus, creds: MCPCredentials): MCPConnectionConfig {
  const generatedEnv = mcpCredentialsToEnv(form.type, creds);
  const generatedKeys = generatedMCPEnvKeys(form.type, generatedEnv);
  const extraEnv = form.env.filter((item) => !generatedKeys.has(envKey(item)));
  const generatedArgs = mcpCredentialsToArgs(form.type, creds) || [];
  return normalizeMCPConnectionConfig({
    id: form.id,
    name: form.name,
    type: form.type,
    transport: form.transport,
    command: form.command,
    args: [...generatedArgs, ...form.args],
    env: [...generatedEnv, ...extraEnv],
    url: form.url,
    enabled: form.enabled,
    containerId: form.containerId,
    nodeletId: form.nodeletId,
  });
}

export function normalizeMCPConnectionConfig(config: MCPConnectionConfig): MCPConnectionConfig {
  const baseConfig: MCPConnectionConfig = {
    id: config.id,
    name: config.name,
    type: config.type,
    transport: config.transport,
    command: config.command,
    args: config.args,
    env: config.env,
    url: config.url,
    enabled: config.enabled,
    containerId: config.containerId,
    nodeletId: config.nodeletId,
  };
  const creds = parseMCPConnectionCredentials(baseConfig.type, baseConfig.env, baseConfig.args);
  const generatedEnv = mcpCredentialsToEnv(config.type, creds);
  if (generatedEnv.length === 0) return baseConfig;
  const generatedKeys = generatedMCPEnvKeys(config.type, generatedEnv);
  const extraEnv = baseConfig.env.filter((item) => !generatedKeys.has(envKey(item)));
  const generatedArgs = mcpCredentialsToArgs(config.type, creds) || [];
  const args = [...generatedArgs, ...stripGeneratedMCPArgs(config.type, baseConfig.args)];
  const env = [...generatedEnv, ...extraEnv];
  if (args.length === baseConfig.args.length && env.length === baseConfig.env.length &&
    args.every((item, index) => item === baseConfig.args[index]) &&
    env.every((item, index) => item === baseConfig.env[index])) {
    return baseConfig;
  }
  return { ...baseConfig, args, env };
}

export function validateMCPCredentials(type: string, creds: MCPCredentials): string {
  if (type === "kafka" && Boolean(creds.user) !== Boolean(creds.password)) {
    return "Kafka 用户名和密码需要同时填写";
  }
  if (type === "kafka" && creds.securityProtocol.startsWith("sasl") && (!creds.user || !creds.password)) {
    return "Kafka 使用 SASL 协议时需要填写用户名和密码";
  }
  return "";
}
