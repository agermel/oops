#!/usr/bin/env bash
set -euo pipefail

name="${1:?usage: sync-mcp-wrapper.sh <elasticsearch|nacos|redis>}"
case "$name" in
	elasticsearch|nacos|redis) ;;
	*)
		echo "unsupported wrapper: $name" >&2
		exit 2
		;;
esac

root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
if [[ -n "${OOPS_MCP_VENV_ROOT:-}" ]]; then
	export UV_PROJECT_ENVIRONMENT="$OOPS_MCP_VENV_ROOT/$name"
fi
exec uv sync --directory "$root/mcp-servers/$name" --locked
