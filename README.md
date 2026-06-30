# Oops — Ops Plane

**AI 驱动的运维管理平台，一面玻璃，全局掌控。**

Oops 提供统一的可视化面板，管理 Docker 主机、监控服务连接、排查基础设施故障 —— 内置 LLM 运维助手，能用自然语言巡检容器、查看日志、诊断问题。

<p align="center">
  <img src="docs/screenshot.png" alt="Oops Dashboard" width="800" />
</p>

---

## 架构

```
┌──────────────────────────────────────────────────┐
│                   Ops Plane                       │
│   (中心服务器 — Web 界面 + API + LLM Agent)        │
│                    :8081                          │
└──────────┬──────────┬──────────┬─────────────────┘
           │ HTTP     │ HTTP     │ HTTP
     ┌─────▼──┐  ┌────▼───┐  ┌──▼──────┐
     │Nodelet │  │Nodelet │  │Nodelet  │  ...  (每个 Docker 主机一个)
     │ :8686  │  │ :8686  │  │ :8686   │
     └────────┘  └────────┘  └─────────┘
```

- **Ops Plane** — 中心服务器，承载 React 前端、管理配置、代理请求、运行 LLM Agent。
- **Nodelet** — 部署在每台 Docker 主机上的轻量 Agent，通过 HTTP 暴露容器列表、巡检、日志流等接口。

## 功能

### 🐳 Docker 与容器管理
- **自动识别服务** — 从容器镜像检测 23 种服务类型（MySQL、Redis、PostgreSQL、MongoDB、Nginx、Elasticsearch、Kafka、Etcd、Jaeger、Nacos、RabbitMQ、ClickHouse、MinIO、Consul、ZooKeeper、Prometheus、Grafana、InfluxDB、Memcached、Cassandra、Neo4j、Caddy）。
- **容器巡检** — 环境变量、端口映射、健康检查、创建时间。
- **实时日志流** — 通过 SSE 实时查看容器日志，支持 ANSI 颜色渲染。
- **DSN 自动检测** — 从容器环境变量中提取连接字符串。
- **HTTP 健康检查** — 按需探测容器端点。

### 🤖 AI 运维助手
- **自然语言排查** — 直接问"Redis 为什么慢？""MySQL 容器里有什么报错？"
- **智能 Agent 路由** — 根据问题类型自动选择合适的 Agent：
  - **Diagnose Agent** — 系统化故障诊断（崩溃、报错、超时）
  - **Inspect Agent** — 快速健康检查和状态概览
  - **Default Agent** — 通用运维问答
- **工具调用** — LLM 可列出容器、查看日志、检查连接、调用 MCP 工具。
- **流式响应** — 实时看到 Agent 的思考过程、工具调用和结果。
- **上下文管理** — 自动 Token 预算控制和消息裁剪，适应长会话。

### 🔌 MCP（模型上下文协议）集成
- 接入社区 MCP Server，扩展 AI 助手的工具集。
- 支持 **stdio**（本地子进程）和 **SSE**（远程）两种传输方式。
- 内置 MCP Server 二进制文件：MySQL、Redis、Elasticsearch、Kafka、Etcd、Nacos。
- 按工具启用/禁用，界面内直接测试工具连通性。
- 自动保活，异常退出自动重启。

### 🏥 连接监控
- 配置外部服务连接（Elasticsearch、HTTP 端点、TCP 端口）。
- 并行健康检查，每个连接独立超时。
- 仪表盘实时状态指示（存活 / 死亡 / 未知）。

### 🔐 认证与安全
- 基于 YAML 的用户存储，bcrypt 密码哈希。
- JWT 会话令牌（HttpOnly Cookie）。
- 登录频率限制，防止暴力破解。
- Nodelet 到 Server 的 Bearer Token 认证。
- 每个 Nodelet API 限流（50 req/s）。

### 📊 可观测性
- **控制台中心** — 集中化日志查看，服务端事件流推送。
- 结构化日志（zap），支持日志轮转。
- MCP 子进程 stderr 捕获。
- Nodelet 请求级日志（鉴权失败、限流触发、CRUD 操作全链路可观测）。

## 快速开始

### 环境要求
- Go 1.25+
- Node.js 20+（构建前端）
- Docker（容器管理）
- OpenAI 兼容的 API Key（AI 助手）

### 1. 克隆 & 构建

```bash
git clone git@github.com:agermel/oops.git
cd oops

# 构建前端
cd web && npm install && npm run build && cd ..

# 构建 Ops Plane 服务端
go build -o oops ./cmd/oops

# 构建 Nodelet（或直接使用 Docker 镜像）
go build -o oops-nodelet ./cmd/oops-nodelet
```

### 2. 配置

```bash
# 复制并编辑示例配置
cp config/config.example.yaml config/config.yaml

# 设置用户凭据
cp data/users.yml.example data/users.yml
# 生成密码哈希：
./oops hash-password

# 添加你的 Nodelet
# 编辑 config/nodelets.json：
# { "nodelets": [{ "id": "local", "name": "本地 Docker", "address": "http://127.0.0.1:8686", "token": "..." }] }
```

### 3. 启动 Nodelet

在每台 Docker 主机上：

```bash
# 直接运行
./oops-nodelet

# 或通过 Docker Compose
cd deployment && docker compose -f docker-compose.nodelet.yml up -d
```

Nodelet 监听 `:8686`，首次运行自动生成认证 Token。

### 4. 启动 Ops Plane

```bash
OOPS_LLM_API_KEY="sk-..." ./oops
```

浏览器打开 `http://localhost:8081`，登录即可。

### 5. （可选）启用 MCP

```bash
# 在 config/config.yaml 中启用 MCP：
# mcp:
#   enabled: true
#   transport: "stdio"
#   command: "mysql-mcp-server"
#   args: ["--read-only"]

# 设置允许的 MCP 命令：
export OOPS_MCP_ALLOWED_COMMANDS="mysql-mcp-server,redis-mcp-server"
```

MCP Server 二进制文件在 `mcp-servers/` 目录下，构建方式：

```bash
cd mcp-servers/mysql && go build -o mysql-mcp-server . && cd ../..
```

## 配置项

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `OOPS_ADDR` | `:8081` | Ops Plane 监听地址 |
| `OOPS_NODELET_ADDR` | `:8686` | Nodelet 监听地址 |
| `OOPS_LLM_API_KEY` | — | OpenAI 兼容的 API Key（配置中支持 `${ENV}` 语法） |
| `OOPS_MCP_ALLOWED_COMMANDS` | — | 逗号分隔的 MCP Server 命令白名单 |

完整配置参考 `config/config.example.yaml`。

## 项目结构

```
oops/
├── cmd/
│   ├── oops/              # 中心 Ops Plane 服务入口
│   └── oops-nodelet/      # Nodelet Agent 入口
├── internal/
│   ├── api/               # HTTP API 服务端与路由
│   ├── auth/              # 认证（JWT、用户存储、限流）
│   ├── config/            # 配置加载（Viper）
│   ├── connection/        # 服务连接健康检查
│   ├── console/           # 日志中心（SSE 推送至浏览器）
│   ├── docker/            # Docker 客户端、服务检测、DSN 提取
│   ├── llm/               # LLM Agent、工具、路由、会话管理
│   ├── mcp/               # MCP 管理器、客户端、工具注册
│   ├── nodelet/           # Nodelet 管理器、探活、服务端
│   └── project/           # 项目 CRUD 与管理
├── web/                   # React SPA（TypeScript, Vite）
├── config/                # 配置文件与 LLM Prompts
├── deployment/            # Nodelet Docker 部署文件
├── mcp-servers/           # 预构建的 MCP Server 二进制文件与源码
└── data/                  # 运行时数据（SQLite 数据库、用户 YAML）
```

## 技术栈

| 组件 | 技术 |
|---|---|
| 后端 | Go 1.25, [Eino](https://github.com/cloudwego/eino)（LLM Agent 框架）, [zap](https://github.com/uber-go/zap)（日志）, [mcp-go](https://github.com/mark3labs/mcp-go) |
| 前端 | React 19, TypeScript, Vite, Lucide 图标 |
| AI | OpenAI 兼容 API（默认 GPT-4o-mini），ReAct Agent 模式 |
| 数据库 | SQLite（纯 Go 实现，事件持久化） |
| 容器 | Docker Engine API（Moby 客户端） |

## License

MIT

---

**Oops** — *基础设施，不出意外。*
