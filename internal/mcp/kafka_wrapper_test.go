package mcp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"oops/internal/config"
)

func TestKafkaWrapperConnectsWhenConfigFileIsGenerated(t *testing.T) {
	fakeBin := filepath.Join(t.TempDir(), "fake-mcp")
	script := `#!/usr/bin/env bash
set -euo pipefail

config_file=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --kafka-config-file)
      config_file="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done

if [[ -z "${config_file}" || ! -f "${config_file}" ]]; then
  echo "missing kafka config file" >&2
  exit 10
fi

if ! grep -q '^security.protocol=sasl_plaintext$' "${config_file}"; then
  echo "missing security protocol" >&2
  exit 11
fi

while IFS= read -r line; do
  case "${line}" in
    *'"method":"initialize"'*)
      printf '%s\n' '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"fake-kafka-mcp","version":"1.0.0"}}}'
      ;;
    *'"method":"notifications/initialized"'*)
      ;;
    *'"method":"tools/list"'*)
      printf '%s\n' '{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"fake-tool","description":"fake tool","inputSchema":{"type":"object","properties":{}}}]}}'
      ;;
    *)
      echo "unexpected input: ${line}" >&2
      exit 10
      ;;
  esac
done
`
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake mcp binary: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, tools, _, closer, err := ConnectWithLog(ctx, config.MCPConfig{
		Transport: "stdio",
		Command:   "../../mcp-servers/kafka/kafka-mcp",
		Env: append(kafkaWrapperEnv(),
			"OOPS_MCP_CONFLUENT_BIN="+fakeBin,
		),
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer closer()

	if len(tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(tools))
	}
}

func kafkaWrapperEnv() []string {
	return []string{
		"BOOTSTRAP_SERVERS=localhost:9092",
		"KAFKA_API_KEY=test",
		"KAFKA_API_SECRET=test",
		"KAFKA_SECURITY_PROTOCOL=sasl_plaintext",
		"KAFKA_SASL_MECHANISM=PLAIN",
	}
}
