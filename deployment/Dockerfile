# Stage 1: Build frontend
FROM node:22-alpine AS frontend
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Stage 2: Build Go server
FROM --platform=$BUILDPLATFORM golang:1.25.5-alpine AS builder
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o /oops ./cmd/oops/

# Stage 3: Build Go-based MCP servers from pinned source.
FROM --platform=$BUILDPLATFORM golang:1.25.5-alpine AS mcp-go-builder
ARG TARGETOS TARGETARCH
WORKDIR /src/mcp-servers/etcd
COPY mcp-servers/etcd/go.mod mcp-servers/etcd/go.sum ./
RUN go mod download
COPY mcp-servers/etcd/ ./
RUN mkdir -p /out && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -o /out/etcd-mcp-server . && \
    go mod download github.com/askdba/mysql-mcp-server@v1.7.1 && \
    cd "$(go env GOMODCACHE)/github.com/askdba/mysql-mcp-server@v1.7.1" && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -o /out/mysql-mcp-server ./cmd/mysql-mcp-server

# Stage 4: Preinstall Node-based MCP servers
FROM node:22-bookworm AS mcp-node
WORKDIR /opt/oops/mcp-confluent
COPY deployment/mcp-confluent/package.json deployment/mcp-confluent/package-lock.json ./
RUN npm ci --omit=dev && npm cache clean --force

# Stage 5: Runtime
FROM node:22-bookworm-slim
ARG TARGETOS TARGETARCH
COPY scripts/fetch-uv.sh /usr/local/lib/oops/fetch-uv.sh
RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates bash curl && \
    /usr/local/lib/oops/fetch-uv.sh "${TARGETOS}/${TARGETARCH}" /usr/local/bin && \
    rm /usr/local/lib/oops/fetch-uv.sh && \
    apt-get purge -y --auto-remove curl && \
    rm -rf /var/lib/apt/lists/* && \
    mkdir -p /var/log/oops /opt/oops/config /opt/oops/mcp-servers /opt/oops/mcp-venvs /opt/oops/uv-python
COPY --from=builder /oops /usr/local/bin/oops
COPY --from=frontend /src/web/dist /opt/oops/web/dist
COPY --from=mcp-node /opt/oops/mcp-confluent /opt/oops/mcp-confluent
COPY mcp-servers/ /opt/oops/mcp-servers/
COPY --from=mcp-go-builder /out/etcd-mcp-server /opt/oops/mcp-servers/etcd/etcd-mcp-server
COPY --from=mcp-go-builder /out/mysql-mcp-server /opt/oops/mcp-servers/mysql/mysql-mcp-server
RUN for wrapper in nacos redis elasticsearch; do \
      UV_PROJECT_ENVIRONMENT="/opt/oops/mcp-venvs/$wrapper" \
      UV_CACHE_DIR=/tmp/uv-cache \
      UV_PYTHON_INSTALL_DIR=/opt/oops/uv-python \
      uv sync --directory "/opt/oops/mcp-servers/$wrapper" --locked --link-mode=copy; \
    done && \
    rm -rf /tmp/uv-cache
ENV OOPS_MCP_VENV_ROOT=/opt/oops/mcp-venvs \
    UV_PYTHON_INSTALL_DIR=/opt/oops/uv-python \
    UV_NO_SYNC=1 \
    UV_OFFLINE=1 \
    UV_PYTHON_DOWNLOADS=never
WORKDIR /opt/oops
EXPOSE 8081
ENTRYPOINT ["oops"]
