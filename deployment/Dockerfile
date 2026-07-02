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

# Stage 3: Runtime
FROM alpine:3.21
RUN apk add --no-cache ca-certificates && \
    mkdir -p /var/log/oops /var/lib/oops /opt/oops/config /opt/oops/mcp-servers
COPY --from=builder /oops /usr/local/bin/oops
COPY --from=frontend /src/web/dist /opt/oops/web/dist
WORKDIR /opt/oops
EXPOSE 8081
ENTRYPOINT ["oops"]
