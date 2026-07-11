#!/usr/bin/env bash
set -euo pipefail

root="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
output="$root/etcd-mcp-server"

if [[ "${1:-}" == "--verify" ]]; then
	go version -m "$output" | grep -q 'oops/mcp-servers/etcd'
	exit 0
fi

cd "$root"
CGO_ENABLED=0 go build -trimpath -o "$output" .
