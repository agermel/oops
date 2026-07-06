import assert from "node:assert/strict";
import test from "node:test";

import type { ProjectMCPConnection } from "../src/types.ts";
import { buildMCPConnectionConfig, normalizeMCPConnectionConfig } from "../src/lib/mcpConfig.ts";

test("normalizes kafka auth env with explicit protocol and mechanism defaults", () => {
  const config = normalizeMCPConnectionConfig({
    id: "kafka-1",
    name: "kafka",
    type: "kafka",
    transport: "stdio",
    command: "./mcp-servers/kafka/kafka-mcp",
    args: [],
    env: [
      "BOOTSTRAP_SERVERS=kafka.example.com:9094",
      "KAFKA_API_KEY=root",
      "KAFKA_API_SECRET=secret",
    ],
    url: "",
    enabled: true,
  });

  assert.ok(config.env.includes("KAFKA_SECURITY_PROTOCOL=sasl_plaintext"));
  assert.ok(config.env.includes("KAFKA_SASL_MECHANISM=PLAIN"));
});

test("keeps explicit kafka protocol and mechanism", () => {
  const config = normalizeMCPConnectionConfig({
    id: "kafka-1",
    name: "kafka",
    type: "kafka",
    transport: "stdio",
    command: "./mcp-servers/kafka/kafka-mcp",
    args: [],
    env: [
      "BOOTSTRAP_SERVERS=kafka.example.com:9094",
      "KAFKA_API_KEY=root",
      "KAFKA_API_SECRET=secret",
      "KAFKA_SECURITY_PROTOCOL=sasl_ssl",
      "KAFKA_SASL_MECHANISM=SCRAM-SHA-512",
    ],
    url: "",
    enabled: true,
  });

  assert.equal(config.env.filter((item) => item.startsWith("KAFKA_SECURITY_PROTOCOL=")).length, 1);
  assert.equal(config.env.filter((item) => item.startsWith("KAFKA_SASL_MECHANISM=")).length, 1);
  assert.ok(config.env.includes("KAFKA_SECURITY_PROTOCOL=sasl_ssl"));
  assert.ok(config.env.includes("KAFKA_SASL_MECHANISM=SCRAM-SHA-512"));
});

test("builds redis config without blank generated env", () => {
  const config = buildMCPConnectionConfig(
    {
      id: "redis-1",
      name: "redis",
      type: "redis",
      transport: "stdio",
      command: "./mcp-servers/redis/redis-mcp-server",
      args: [],
      env: ["REDIS_HOST=old-host", "REDIS_PASSWORD=old-secret", "CUSTOM_FLAG=1"],
      url: "",
      enabled: true,
      status: "stopped",
      toolCount: 0,
    },
    {
      host: "redis.example.com",
      port: "6379",
      user: "",
      password: "secret",
      database: "0",
      securityProtocol: "",
      saslMechanism: "",
    },
  );

  assert.deepEqual(config.env, [
    "REDIS_HOST=redis.example.com",
    "REDIS_PORT=6379",
    "REDIS_DB=0",
    "REDIS_PWD=secret",
    "CUSTOM_FLAG=1",
  ]);
  assert.equal(config.env.some((item) => item === "REDIS_USERNAME="), false);
});

test("normalizes redis password alias into canonical env", () => {
  const config = normalizeMCPConnectionConfig({
    id: "redis-1",
    name: "redis",
    type: "redis",
    transport: "stdio",
    command: "./mcp-servers/redis/redis-mcp-server",
    args: [],
    env: [
      "REDIS_HOST=redis.example.com",
      "REDIS_USERNAME=",
      "REDIS_PASSWORD=secret",
      "EXTRA=1",
    ],
    url: "",
    enabled: true,
  });

  assert.deepEqual(config.env, [
    "REDIS_HOST=redis.example.com",
    "REDIS_PWD=secret",
    "EXTRA=1",
  ]);
});

test("normalizes nacos legacy host args into the saved env shape", () => {
  const config = normalizeMCPConnectionConfig({
    id: "nacos-1",
    name: "nacos",
    type: "nacos",
    transport: "stdio",
    command: "./mcp-servers/nacos/nacos-mcp-server",
    args: ["--host", "nacos.example.com", "--port", "8848", "--debug"],
    env: ["NACOS_USERNAME=nacos", "NACOS_PASSWORD=secret", "EXTRA=1"],
    url: "",
    enabled: true,
  });

  assert.deepEqual(config.args, ["--debug"]);
  assert.deepEqual(config.env, [
    "NACOS_ADDR=nacos.example.com:8848",
    "NACOS_USERNAME=nacos",
    "NACOS_PASSWORD=secret",
    "EXTRA=1",
  ]);
});

test("normalization returns a pure connection config payload", () => {
  const runtimeConfig: ProjectMCPConnection = {
    id: "redis-1",
    name: "redis",
    type: "redis",
    transport: "stdio",
    command: "./mcp-servers/redis/redis-mcp-server",
    args: [],
    env: ["REDIS_HOST=redis.example.com"],
    url: "",
    enabled: true,
    status: "running",
    toolCount: 3,
    scope: "nodelet",
  };
  const config = normalizeMCPConnectionConfig(runtimeConfig);

  assert.equal("status" in config, false);
  assert.equal("toolCount" in config, false);
  assert.equal("scope" in config, false);
});
