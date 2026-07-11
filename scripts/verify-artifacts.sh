#!/usr/bin/env bash
set -euo pipefail

root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
manifest="$root/mcp-servers/artifacts-manifest.json"
only_artifact=""

usage() {
	echo "usage: verify-artifacts.sh [--artifact <name>]" >&2
}

if [[ $# -gt 0 ]]; then
	if [[ $# -ne 2 || "$1" != "--artifact" ]]; then
		usage
		exit 2
	fi
	only_artifact="$2"
fi

jq -e '
  def nonempty_string: type == "string" and length > 0;
  def integrity_kind:
    type == "string"
    and test("^(go-source-tree|go-module-zip|python-wheel|release-archive|python-standalone-archive|oci-image-index)$");
  .artifacts
  | type == "array" and length > 0
  and all(.[];
      type == "object"
      and (.artifact | nonempty_string)
      and (.source | nonempty_string)
      and (.version | nonempty_string)
      and (.platform | type == "string" and test("^(any|[a-z0-9]+)/(any|[a-z0-9_]+)$"))
      and (.integrity_kind | integrity_kind)
      and (.sha256 | type == "string" and test("^[0-9a-f]{64}$"))
      and (.license | type == "string" and test("^[A-Za-z0-9.()+ -]+$"))
      and (.build_or_fetch | nonempty_string)
      and (.verify | nonempty_string)
      and (
        if .integrity_kind == "go-module-zip" then
          (.module | type == "string" and test("^[A-Za-z0-9._/-]+$"))
          and (.revision | type == "string" and test("^[0-9a-f]{40}$"))
        elif .integrity_kind == "python-standalone-archive" then
          (.build | type == "string" and test("^[0-9]{8}$"))
          and (.target | type == "string" and test("^cpython-[0-9]+\\.[0-9]+\\.[0-9]+-linux-(x86_64|aarch64)-gnu$"))
          and (.archive | type == "string" and endswith(".tar.gz"))
        elif .integrity_kind == "oci-image-index" then
          (.image | type == "string" and test("^[a-z0-9][a-z0-9._/-]*:[A-Za-z0-9][A-Za-z0-9._-]*$"))
        else
          true
        end
      )
  )
' "$manifest" >/dev/null

jq -e '
  [.artifacts[] | [.artifact, .platform] | @tsv] as $keys
  | ($keys | length) == ($keys | unique | length)
' "$manifest" >/dev/null

if [[ -n "$only_artifact" ]]; then
	jq -e --arg artifact "$only_artifact" '
		[.artifacts[] | select(.artifact == $artifact)] | length == 1
	' "$manifest" >/dev/null
fi

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

should_verify() {
	[[ -z "$only_artifact" || "$only_artifact" == "$1" ]]
}

sha256_file() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	else
		shasum -a 256 "$1" | awk '{print $1}'
	fi
}

sha256_stream() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum | awk '{print $1}'
	else
		shasum -a 256 | awk '{print $1}'
	fi
}

source_tree_hash() {
	local directory="$1"

	(
		cd "$root"
		LC_ALL=C find "$directory" -type f \
			\( -name '*.go' -o -name go.mod -o -name go.sum -o -name build.sh \) \
			! -name '*_test.go' -print \
			| LC_ALL=C sort \
			| while IFS= read -r file_path; do
				printf '%s\0' "$file_path"
				sha256_file "$file_path"
				printf '\n'
			done
	) | sha256_stream
}

verify_go_source_tree() {
	local artifact="$1"
	local source expected actual

	source="$(manifest_value "$artifact" source)"
	expected="$(manifest_value "$artifact" sha256)"
	if [[ "$source" == /* || "$source" == *".."* || ! -d "$root/$source" ]]; then
		echo "invalid source tree for $artifact" >&2
		return 1
	fi
	actual="$(source_tree_hash "$source")"
	if [[ "$actual" != "$expected" ]]; then
		echo "$artifact source tree checksum mismatch: got $actual" >&2
		return 1
	fi
}

verify_go_module_zip() {
	local artifact="$1"
	local module version revision expected metadata zip actual actual_version actual_revision

	module="$(manifest_value "$artifact" module)"
	version="$(manifest_value "$artifact" version)"
	revision="$(manifest_value "$artifact" revision)"
	expected="$(manifest_value "$artifact" sha256)"
	metadata="$(go mod download -json "$module@$version")"
	zip="$(jq -er '.Zip' <<<"$metadata")"
	actual_version="$(jq -er '.Version' <<<"$metadata")"
	actual_revision="$(jq -er '.Origin.Hash' <<<"$metadata")"
	actual="$(sha256_file "$zip")"
	if [[ "$actual_version" != "$version" || "$actual_revision" != "$revision" || "$actual" != "$expected" ]]; then
		echo "$artifact module source checksum mismatch" >&2
		return 1
	fi
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
	if ! awk \
		-v package="$package" \
		-v version="$version" \
		-v hash="$hash" '
		function check() {
			if (name == package && lockedVersion == version && hasHash) {
				matched = 1
			}
		}
		/^\[\[package\]\]$/ {
			check()
			name = ""
			lockedVersion = ""
			hasHash = 0
			next
		}
		$0 == "name = \"" package "\"" {
			name = package
		}
		$0 == "version = \"" version "\"" {
			lockedVersion = version
		}
		index($0, "sha256:" hash) {
			hasHash = 1
		}
		END {
			check()
			exit !matched
		}
	' "$root/mcp-servers/$wrapper/uv.lock"; then
		echo "manifest hash missing from $wrapper package wheel" >&2
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

verify_cpython_runtime_lock() {
	local platform="$1"
	local source version build target archive hash expected_source wrapper

	source="$(manifest_value cpython source "$platform")"
	version="$(manifest_value cpython version "$platform")"
	build="$(manifest_value cpython build "$platform")"
	target="$(manifest_value cpython target "$platform")"
	archive="$(manifest_value cpython archive "$platform")"
	hash="$(manifest_value cpython sha256 "$platform")"
	expected_source="https://github.com/astral-sh/python-build-standalone/releases/download/$build/$archive"
	if [[ "$source" != "$expected_source" ]]; then
		echo "manifest CPython source does not match its build and archive for $platform" >&2
		return 1
	fi
	for wrapper in nacos redis elasticsearch; do
		if ! rg -Fq "requires-python = \"==$version\"" "$root/mcp-servers/$wrapper/pyproject.toml" || \
			! rg -Fq "requires-python = \"==$version\"" "$root/mcp-servers/$wrapper/uv.lock"; then
			echo "CPython version does not match $wrapper lock" >&2
			return 1
		fi
	done
	if ! rg -Fq "version=\"$version\"" "$root/scripts/fetch-python.sh" || \
		! rg -Fq "build=\"$build\"" "$root/scripts/fetch-python.sh" || \
		! rg -Fq "target=\"$target\"" "$root/scripts/fetch-python.sh" || \
		! rg -Fq "archive=\"$archive\"" "$root/scripts/fetch-python.sh" || \
		! rg -Fq "expected=\"$hash\"" "$root/scripts/fetch-python.sh"; then
		echo "manifest CPython data does not match fetch script for $platform" >&2
		return 1
	fi
	if ! rg -Fq '/usr/local/lib/oops/fetch-python.sh "${TARGETOS}/${TARGETARCH}" /opt/oops/uv-python' "$root/deployment/Dockerfile" || \
		! rg -Fq "UV_PYTHON=$version" "$root/deployment/Dockerfile" || \
		! rg -Fq 'UV_MANAGED_PYTHON=1' "$root/deployment/Dockerfile"; then
		echo "Docker runtime does not select the locked CPython interpreter" >&2
		return 1
	fi
}

verify_oci_image_lock() {
	local artifact="$1"
	shift
	local image hash reference file_path

	image="$(manifest_value "$artifact" image)"
	hash="$(manifest_value "$artifact" sha256)"
	reference="$image@sha256:$hash"
	for file_path in "$@"; do
		if ! rg -Fq "FROM $reference" "$root/$file_path" && \
			! rg -Fq "FROM --platform=\$BUILDPLATFORM $reference" "$root/$file_path"; then
			echo "$artifact digest is missing from $file_path" >&2
			return 1
		fi
	done
}

verify_dockerfile_base_images() {
	local file_path

	for file_path in deployment/Dockerfile deployment/Dockerfile.nodelet; do
		if ! awk '
			/^FROM[[:space:]]/ {
				image = ""
				for (field_index = 2; field_index <= NF; field_index++) {
					if ($field_index ~ /^--platform=/) {
						continue
					}
					if ($field_index == "AS") {
						break
					}
					image = $field_index
					break
				}
				if (image == "" || image !~ /@sha256:[0-9a-f]+$/) {
					print FILENAME ": unpinned Docker base image: " $0 > "/dev/stderr"
					invalid = 1
				}
			}
			END { exit invalid }
		' "$root/$file_path"; then
			return 1
		fi
	done
}

if should_verify etcd-mcp-server; then
	verify_go_source_tree etcd-mcp-server
fi
if should_verify mysql-mcp-server; then
	verify_go_module_zip mysql-mcp-server
fi
if should_verify nacos-mcp-router-wheel; then
	verify_wrapper_lock nacos-mcp-router-wheel nacos nacos-mcp-router
fi
if should_verify redis-mcp-server-wheel; then
	verify_wrapper_lock redis-mcp-server-wheel redis redis-mcp-server
fi
if should_verify elasticsearch-mcp-server-wheel; then
	verify_wrapper_lock elasticsearch-mcp-server-wheel elasticsearch elasticsearch-mcp-server
fi
if should_verify node-22-alpine-image; then
	verify_oci_image_lock node-22-alpine-image deployment/Dockerfile
fi
if should_verify golang-1.25.5-alpine-image; then
	verify_oci_image_lock golang-1.25.5-alpine-image deployment/Dockerfile deployment/Dockerfile.nodelet
fi
if should_verify node-22-bookworm-image; then
	verify_oci_image_lock node-22-bookworm-image deployment/Dockerfile
fi
if should_verify node-22-bookworm-slim-image; then
	verify_oci_image_lock node-22-bookworm-slim-image deployment/Dockerfile
fi
if should_verify alpine-3.21-image; then
	verify_oci_image_lock alpine-3.21-image deployment/Dockerfile.nodelet
fi
if [[ -z "$only_artifact" ]]; then
	verify_uv_fetch_hash linux/amd64
	verify_uv_fetch_hash linux/arm64
	verify_uv_fetch_hash darwin/arm64
	verify_cpython_runtime_lock linux/amd64
	verify_cpython_runtime_lock linux/arm64
	verify_dockerfile_base_images

	found=0
	while IFS= read -r -d '' file_path; do
		kind="$(file -b "$root/$file_path")"
		case "$kind" in
			*Mach-O*|*ELF*|*PE32*)
				echo "tracked architecture binary: $file_path ($kind)" >&2
				found=1
				;;
		esac
	done < <(git -C "$root" ls-files -z)

	exit "$found"
fi
