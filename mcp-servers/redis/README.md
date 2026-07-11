# Redis MCP Server

第三方 MCP server，来自 [redis/mcp-redis](https://github.com/redis/mcp-redis)（MIT）。

一个 Redis MCP server（Python 编写），通过独立 Python 3.12 环境和固定 `uv.lock` 运行。

## 工具

`redis_get`, `redis_set`, `redis_del`, `redis_keys`, `redis_type`, `redis_ttl`, `redis_info` 等。

## 接入 oops

```json
{
  "id": "redis-xxx",
  "name": "Redis",
  "type": "redis",
  "command": "./mcp-servers/redis/redis-mcp-server",
  "args": [],
  "env": [
    "REDIS_URL=redis://127.0.0.1:6379",
    "REDIS_PWD=password"
  ],
  "enabled": true
}
```

## 同步与校验

```bash
./scripts/sync-mcp-wrapper.sh redis
OOPS_MCP_VENV_ROOT="$PWD/.mcp-venvs" ./mcp-servers/redis/redis-mcp-server --help
```

包装器固定 `redis-mcp-server==0.5.0`。来源、许可证与 wheel SHA-256 记录见 `mcp-servers/artifacts-manifest.json`。
