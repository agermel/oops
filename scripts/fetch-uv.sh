#!/usr/bin/env bash
set -euo pipefail

platform="${1:?usage: fetch-uv.sh <goos/goarch> [output-directory]}"
output="${2:-/usr/local/bin}"
version="0.11.28"

case "$platform" in
	linux/amd64)
		target="x86_64-unknown-linux-gnu"
		expected="e490a6464492183c5d4534a5527fb4440f7f2bb2f228162ad7e4afe076dc0224"
		;;
	linux/arm64)
		target="aarch64-unknown-linux-gnu"
		expected="03e9fe0a81b0718d0bc84625de3885df6cc3f89a8b6af6121d6b9f6113fb6533"
		;;
	darwin/arm64)
		target="aarch64-apple-darwin"
		expected="33540eb7c883ab857eff79bd5ac2aa31fe27b595abecb4a9c003a2c998447232"
		;;
	*)
		echo "unsupported platform: $platform" >&2
		exit 2
		;;
esac

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
archive="$work/uv.tar.gz"
url="https://github.com/astral-sh/uv/releases/download/$version/uv-$target.tar.gz"

curl -fsSL --retry 3 --output "$archive" "$url"
if command -v sha256sum >/dev/null 2>&1; then
	actual="$(sha256sum "$archive" | awk '{print $1}')"
else
	actual="$(shasum -a 256 "$archive" | awk '{print $1}')"
fi
if [[ "$actual" != "$expected" ]]; then
	echo "uv checksum mismatch: got $actual" >&2
	exit 1
fi

tar -xzf "$archive" -C "$work"
install -d "$output"
install -m 0755 "$work/uv-$target/uv" "$output/uv"
install -m 0755 "$work/uv-$target/uvx" "$output/uvx"
