#!/usr/bin/env bash
set -euo pipefail

root="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
output="$root/mysql-mcp-server"
version="v1.7.1"

if [[ "${1:-}" == "--verify" ]]; then
	go version -m "$output" | grep -q "github.com/askdba/mysql-mcp-server $version"
	exit 0
fi

GOBIN="$root" go install "github.com/askdba/mysql-mcp-server/cmd/mysql-mcp-server@$version"
