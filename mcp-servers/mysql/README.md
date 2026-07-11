# MySQL MCP Server

第三方 MCP server，来自 [askdba/mysql-mcp-server](https://github.com/askdba/mysql-mcp-server)（Apache 2.0）。

一个快速、只读的 MySQL MCP server（Go 编写），向 AI 助手暴露安全的 MySQL 内省工具。

## 工具

`list_databases`, `list_tables`, `describe_table`, `run_query`, `ping`, `server_info`, `list_connections`, `use_connection` 等。

## 接入 oops

```json
{
  "id": "mysql-xxx",
  "name": "MySQL",
  "type": "mysql",
  "command": "./mcp-servers/mysql/mysql-mcp-server",
  "args": [],
  "env": ["MYSQL_DSN=user:pass@tcp(host:3306)/db"],
  "enabled": true
}
```

## 构建

```bash
./mcp-servers/mysql/build.sh
```

脚本固定构建 `v1.7.1`，来源、许可、平台与校验记录见 `mcp-servers/artifacts-manifest.json`。
