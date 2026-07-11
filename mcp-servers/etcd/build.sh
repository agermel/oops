#!/usr/bin/env bash
set -euo pipefail

root="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
output="$root/etcd-mcp-server"

verify_source() {
	"$root/../../scripts/verify-artifacts.sh" --artifact etcd-mcp-server
}

if [[ "${1:-}" == "--verify" ]]; then
	verify_source
	go version -m "$output" | grep -q 'oops/mcp-servers/etcd'
	exit 0
fi

verify_source
cd "$root"
CGO_ENABLED=0 go build -trimpath -o "$output" .
