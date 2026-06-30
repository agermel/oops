# Oops — Ops Plane

**AI-powered infrastructure operations management, from a single pane of glass.**

Oops gives you a unified web dashboard to manage Docker hosts, monitor service connections, and troubleshoot infrastructure — with an LLM-powered ops assistant that can inspect containers, read logs, and diagnose problems in plain English.

<p align="center">
  <img src="docs/screenshot.png" alt="Oops Dashboard" width="800" />
</p>

---

## Architecture

```
┌──────────────────────────────────────────────────┐
│                   Ops Plane                       │
│  (central server — web UI + API + LLM agent)     │
│                    :8081                          │
└──────────┬──────────┬──────────┬─────────────────┘
           │ HTTP     │ HTTP     │ HTTP
     ┌─────▼──┐  ┌────▼───┐  ┌──▼──────┐
     │Nodelet │  │Nodelet │  │Nodelet  │  ...  (one per Docker host)
     │ :8686  │  │ :8686  │  │ :8686   │
     └────────┘  └────────┘  └─────────┘
```

- **Ops Plane** — Central server that serves the React dashboard, manages configuration, proxies requests, and runs the LLM agent.
- **Nodelet** — Lightweight agent deployed on each Docker host. Exposes container listing, inspection, and log streaming over HTTP.

## Features

### 🐳 Docker & Container Management
- **Auto-discover services** — 23 service types detected from container images (MySQL, Redis, PostgreSQL, MongoDB, Nginx, Elasticsearch, Kafka, Etcd, Jaeger, Nacos, RabbitMQ, ClickHouse, MinIO, Consul, ZooKeeper, Prometheus, Grafana, InfluxDB, Memcached, Cassandra, Neo4j, Caddy).
- **Container inspection** — environment variables, port mappings, health checks, creation time.
- **Real-time log streaming** — view container logs as they happen via SSE, with ANSI color rendering.
- **DSN auto-detection** — extract connection strings from container environment variables.
- **HTTP health checks** — probe container endpoints on demand.

### 🤖 AI Ops Assistant
- **Natural-language troubleshooting** — ask questions like "Why is Redis slow?" or "What errors are in the MySQL container?"
- **Intelligent agent routing** — automatically picks the right agent type based on your question:
  - **Diagnose Agent** — systematic fault diagnosis (crashes, errors, timeouts)
  - **Inspect Agent** — quick health checks and status overviews
  - **Default Agent** — general operations questions
- **Tool-calling** — the LLM can list containers, read logs, check connections, and use MCP tools.
- **Streaming responses** — see the agent's thinking, tool calls, and results in real time.
- **Context window management** — automatic token budgeting and message trimming for long sessions.

### 🔌 MCP (Model Context Protocol) Integration
- Connect to community MCP servers to extend the AI assistant's toolset.
- Supports **stdio** transport (local subprocess) and **SSE** transport (remote).
- Pre-built MCP server binaries included: MySQL, Redis, Elasticsearch, Kafka, Etcd, Nacos.
- Per-tool enable/disable toggling and in-UI tool testing.
- Automatic keepalive with restart on failure.

### 🏥 Connection Monitoring
- Configure external service connections (Elasticsearch, HTTP endpoints, TCP ports).
- Parallel health checking with per-connection timeouts.
- Live status indicators in the dashboard (alive / dead / unknown).

### 🔐 Authentication & Security
- YAML-based user store with bcrypt password hashing.
- JWT-based session tokens (HttpOnly cookies).
- Login rate limiting to prevent brute force.
- Nodelet-to-server authentication via Bearer tokens.
- Per-nodelet API rate limiting (50 req/s).

### 📊 Observability
- **Console hub** — centralized log viewer with server-side event streaming.
- Structured logging (zap) with log rotation.
- MCP subprocess stderr capture.
- Request-level logging for nodelet (auth failures, rate-limit hits, CRUD operations).

## Quick Start

### Prerequisites
- Go 1.25+
- Node.js 20+ (for building the frontend)
- Docker (for container management)
- An OpenAI-compatible API key (for the AI assistant)

### 1. Clone & Build

```bash
git clone git@github.com:agermel/oops.git
cd oops

# Build frontend
cd web && npm install && npm run build && cd ..

# Build ops plane server
go build -o oops ./cmd/oops

# Build nodelet (or use the Docker image)
go build -o oops-nodelet ./cmd/oops-nodelet
```

### 2. Configure

```bash
# Copy and edit the example config
cp config/config.example.yaml config/config.yaml

# Set up your user credentials
cp data/users.yml.example data/users.yml
# Generate a password hash:
./oops hash-password

# Add your nodelets
# Edit config/nodelets.json:
# { "nodelets": [{ "id": "local", "name": "Local Docker", "address": "http://127.0.0.1:8686", "token": "..." }] }
```

### 3. Start the Nodelet

On each Docker host:

```bash
# Run directly
./oops-nodelet

# Or via Docker Compose
cd deployment && docker compose -f docker-compose.nodelet.yml up -d
```

The nodelet listens on `:8686` and auto-generates its auth token on first run.

### 4. Start the Ops Plane

```bash
OOPS_LLM_API_KEY="sk-..." ./oops
```

Open `http://localhost:8081` in your browser and log in.

### 5. (Optional) Enable MCP

```bash
# Enable MCP in config/config.yaml:
# mcp:
#   enabled: true
#   transport: "stdio"
#   command: "mysql-mcp-server"
#   args: ["--read-only"]

# Set allowed MCP commands:
export OOPS_MCP_ALLOWED_COMMANDS="mysql-mcp-server,redis-mcp-server"
```

MCP server binaries are in `mcp-servers/`. Build them with:

```bash
cd mcp-servers/mysql && go build -o mysql-mcp-server . && cd ../..
```

## Configuration

| Env Variable | Default | Description |
|---|---|---|
| `OOPS_ADDR` | `:8081` | Ops plane listen address |
| `OOPS_NODELET_ADDR` | `:8686` | Nodelet listen address |
| `OOPS_LLM_API_KEY` | — | OpenAI-compatible API key (supports `${ENV}` syntax in config) |
| `OOPS_MCP_ALLOWED_COMMANDS` | — | Comma-separated allowlist of MCP server commands |

See `config/config.example.yaml` for the full configuration reference.

## Project Structure

```
oops/
├── cmd/
│   ├── oops/              # Central ops plane server entry point
│   └── oops-nodelet/      # Nodelet agent entry point
├── internal/
│   ├── api/               # HTTP API server & routes
│   ├── auth/              # Authentication (JWT, user store, rate limiting)
│   ├── config/            # Configuration loading (Viper)
│   ├── connection/        # Service connection health checking
│   ├── console/           # Log hub (SSE streaming to browser)
│   ├── docker/            # Docker client, service detection, DSN extraction
│   ├── llm/               # LLM agent, tools, router, session management
│   ├── mcp/               # MCP manager, client, tool registry
│   ├── nodelet/           # Nodelet manager, prober, server
│   └── project/           # Project CRUD & management
├── web/                   # React SPA (TypeScript, Vite)
├── config/                # Config files & LLM prompts
├── deployment/            # Docker deployment files for nodelet
├── mcp-servers/           # Pre-built MCP server binaries & sources
└── data/                  # Runtime data (SQLite DB, users YAML)
```

## Tech Stack

| Component | Technology |
|---|---|
| Backend | Go 1.25, [Eino](https://github.com/cloudwego/eino) (LLM agent framework), [zap](https://github.com/uber-go/zap) (logging), [mcp-go](https://github.com/mark3labs/mcp-go) |
| Frontend | React 19, TypeScript, Vite, Lucide Icons |
| AI | OpenAI-compatible API (GPT-4o-mini by default), ReAct agent pattern |
| Database | SQLite (pure Go, for event persistence) |
| Container | Docker Engine API via Moby client |

## License

MIT

---

**Oops** — *because infrastructure shouldn't be a surprise.*
