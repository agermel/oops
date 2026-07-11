#!/usr/bin/env bash
set -euo pipefail

root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
manifest="$root/mcp-servers/artifacts-manifest.json"

jq -e '
  def nonempty_string: type == "string" and length > 0;
  .artifacts
  | type == "array" and length > 0
  and all(.[];
      type == "object"
      and (.artifact | nonempty_string)
      and (.source | nonempty_string)
      and (.version | nonempty_string)
      and (.platform | type == "string" and test("^(any|[a-z0-9]+)/(any|[a-z0-9_]+)$"))
      and (.sha256 | type == "string" and test("^[0-9a-f]{64}$"))
      and (.license | type == "string" and test("^[A-Za-z0-9.()+ -]+$"))
      and (.build_or_fetch | nonempty_string)
      and (.verify | nonempty_string)
  )
' "$manifest" >/dev/null

jq -e '
  [.artifacts[] | [.artifact, .platform] | @tsv] as $keys
  | ($keys | length) == ($keys | unique | length)
' "$manifest" >/dev/null

manifest_value() {
	local artifact="$1"
	local field="$2"
	local platform="${3:-}"

	jq -er \
		--arg artifact "$artifact" \
		--arg field "$field" \
		--arg platform "$platform" '
		[.artifacts[] | select(.artifact == $artifact and ($platform == "" or .platform == $platform))] as $matches
		| if ($matches | length) == 1 then $matches[0][$field] else error("expected one matching artifact") end
	' "$manifest"
}

verify_wrapper_lock() {
	local artifact="$1"
	local wrapper="$2"
	local package="$3"
	local hash version

	hash="$(manifest_value "$artifact" sha256)"
	version="$(manifest_value "$artifact" version)"
	if ! rg -Fq "$package==$version" "$root/mcp-servers/$wrapper/pyproject.toml"; then
		echo "manifest version does not match $wrapper wrapper" >&2
		return 1
	fi
	if ! rg -Fq "sha256:$hash" "$root/mcp-servers/$wrapper/uv.lock"; then
		echo "manifest hash missing from $wrapper lockfile" >&2
		return 1
	fi
}

verify_uv_fetch_hash() {
	local platform="$1"
	local hash version

	hash="$(manifest_value uv sha256 "$platform")"
	version="$(manifest_value uv version "$platform")"
	if ! rg -Fq "version=\"$version\"" "$root/scripts/fetch-uv.sh"; then
		echo "manifest version does not match uv fetch script" >&2
		return 1
	fi
	if ! rg -Fq "expected=\"$hash\"" "$root/scripts/fetch-uv.sh"; then
		echo "manifest hash missing from uv fetch script for $platform" >&2
		return 1
	fi
}

verify_wrapper_lock nacos-mcp-router-wheel nacos nacos-mcp-router
verify_wrapper_lock redis-mcp-server-wheel redis redis-mcp-server
verify_wrapper_lock elasticsearch-mcp-server-wheel elasticsearch elasticsearch-mcp-server
verify_uv_fetch_hash linux/amd64
verify_uv_fetch_hash linux/arm64
verify_uv_fetch_hash darwin/arm64

found=0
while IFS= read -r -d '' path; do
	kind="$(file -b "$root/$path")"
	case "$kind" in
		*Mach-O*|*ELF*|*PE32*)
			echo "tracked architecture binary: $path ($kind)" >&2
			found=1
			;;
	esac
done < <(git -C "$root" ls-files -z)

exit "$found"
