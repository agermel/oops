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
  "args": ["--silent"],
  "env": ["MYSQL_DSN=user:pass@tcp(host:3306)/db"],
  "enabled": true
}
```

## 更新

```bash
brew upgrade mysql-mcp-server
# 或从 GitHub Releases 下载最新二进制替换
```
