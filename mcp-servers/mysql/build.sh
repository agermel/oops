#!/usr/bin/env bash
set -euo pipefail

root="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
output="$root/mysql-mcp-server"
module="github.com/askdba/mysql-mcp-server"
version="v1.7.1"

verify_source() {
	"$root/../../scripts/verify-artifacts.sh" --artifact mysql-mcp-server
}

if [[ "${1:-}" == "--verify" ]]; then
	verify_source
	go version -m "$output" | grep -q "github.com/askdba/mysql-mcp-server $version"
	exit 0
fi

verify_source
GOBIN="$root" go install "$module/cmd/mysql-mcp-server@$version"
