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
