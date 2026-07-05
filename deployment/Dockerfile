# Stage 1: Build frontend
FROM node:22-alpine AS frontend
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Stage 2: Build Go server
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o /oops ./cmd/oops/

# Stage 3: Preinstall Node-based MCP servers
FROM node:22-bookworm AS mcp-node
WORKDIR /opt/oops/mcp-confluent
COPY deployment/mcp-confluent/package.json deployment/mcp-confluent/package-lock.json ./
RUN npm ci --omit=dev && npm cache clean --force

# Stage 4: Runtime
FROM node:22-bookworm-slim
RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates bash && \
    rm -rf /var/lib/apt/lists/* && \
    mkdir -p /var/log/oops /var/lib/oops /opt/oops/config /opt/oops/mcp-servers
COPY --from=builder /oops /usr/local/bin/oops
COPY --from=frontend /src/web/dist /opt/oops/web/dist
COPY --from=mcp-node /opt/oops/mcp-confluent /opt/oops/mcp-confluent
WORKDIR /opt/oops
EXPOSE 8081
ENTRYPOINT ["oops"]
