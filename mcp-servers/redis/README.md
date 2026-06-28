# Redis MCP Server

第三方 MCP server，来自 [redis/mcp-redis](https://github.com/redis/mcp-redis)（MIT）。

一个 Redis MCP server（Python 编写），通过 `uvx` 运行，自动下载和缓存。

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

## 更新

```bash
uvx --from redis-mcp-server@latest redis-mcp-server
```
