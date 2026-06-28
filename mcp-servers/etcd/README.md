# etcd MCP Server

etcd v3 key-value store 的 Model Context Protocol (MCP) server。支持通过 AI 助手（Claude、Cursor 等）或 oops 项目以自然语言操作 etcd 集群。

## 安装

```bash
cd mcp-servers/etcd
go build -o etcd-mcp-server .
```

## 配置

通过环境变量连接 etcd：

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `ETCD_ENDPOINTS` | `localhost:2379` | 逗号分隔的 etcd 端点 |
| `ETCD_USERNAME` | — | etcd 用户名（可选） |
| `ETCD_PASSWORD` | — | etcd 密码（可选） |
| `ETCD_DIAL_TIMEOUT` | `5s` | 连接超时时间 |

## 工具列表

| 工具 | 说明 |
|------|------|
| `etcd_health` | 检查集群健康状态，返回版本、DB 大小、Leader 信息 |
| `etcd_get` | 获取 key-value，支持前缀扫描、范围查询、排序、keys-only |
| `etcd_put` | 写入 key-value，可选关联 Lease 实现自动过期 |
| `etcd_delete` | 删除 key，支持前缀批量删除 |
| `etcd_list` | 按前缀列出所有 key（仅返回 key 名，不返回值） |
| `etcd_watch` | 监听 key/前缀 变化，返回第一批事件 |
| `etcd_status` | 集群状态：成员列表 + 各端点详细状态 |
| `etcd_lease` | 管理租约：grant / revoke / list / timetolive |

## 接入 oops

在 `config/mcp_connections.json` 或 MCP 管理面板中添加：

```json
{
  "id": "etcd-local",
  "name": "Etcd Local",
  "type": "etcd",
  "command": "./mcp-servers/etcd/etcd-mcp-server",
  "args": [],
  "env": [
    "ETCD_ENDPOINTS=127.0.0.1:2379",
    "ETCD_USERNAME=root",
    "ETCD_PASSWORD=password"
  ],
  "enabled": true
}
```

## 接入 Claude Desktop

在 `~/.claude/claude_desktop_config.json` 中添加：

```json
{
  "mcpServers": {
    "etcd": {
      "command": "/path/to/mcp-servers/etcd/etcd-mcp-server",
      "env": {
        "ETCD_ENDPOINTS": "127.0.0.1:2379",
        "ETCD_USERNAME": "root",
        "ETCD_PASSWORD": "password"
      }
    }
  }
}
```

## 许可证

与 oops 项目一致。
