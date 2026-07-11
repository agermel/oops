#!/usr/bin/env bash
set -euo pipefail

platform="${1:?usage: fetch-python.sh <goos/goarch> [install-directory]}"
install_dir="${2:-/opt/oops/uv-python}"
version="3.12.13"
build="20260623"
mirror="https://github.com/astral-sh/python-build-standalone/releases/download"

case "$platform" in
	linux/amd64)
		target="cpython-3.12.13-linux-x86_64-gnu"
		archive="cpython-3.12.13%2B20260623-x86_64-unknown-linux-gnu-install_only_stripped.tar.gz"
		expected="10a452caac7041357805f0c19a60576df53f1ab06d1abfc9200f1f0157cb3bd1"
		;;
	linux/arm64)
		target="cpython-3.12.13-linux-aarch64-gnu"
		archive="cpython-3.12.13%2B20260623-aarch64-unknown-linux-gnu-install_only_stripped.tar.gz"
		expected="b85154b9c7ca9de3f85f2c9f032d503151db16ef198de86b885fc61890c075ed"
		;;
	*)
		echo "unsupported platform: $platform" >&2
		exit 2
		;;
esac

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
checksums="$work/SHA256SUMS"

curl -fsSL --retry 3 --output "$checksums" "$mirror/$build/SHA256SUMS"
checksum_name="${archive//%2B/+}"
published="$(awk -v archive="$checksum_name" '$2 == archive {print $1}' "$checksums")"
if [[ "$published" != "$expected" ]]; then
	echo "CPython release checksum mismatch for $platform" >&2
	exit 1
fi

UV_PYTHON_CPYTHON_BUILD="$build" \
	uv python install --no-bin --install-dir "$install_dir" --mirror "$mirror" "$target"
