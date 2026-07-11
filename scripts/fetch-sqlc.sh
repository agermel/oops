#!/usr/bin/env bash
set -euo pipefail

platform="${1:?usage: fetch-sqlc.sh <goos/goarch> [output-directory]}"
output="${2:-/usr/local/bin}"
version="1.31.1"

case "$platform" in
	linux/amd64)
		archive="sqlc_${version}_linux_amd64.tar.gz"
		expected="497ae4fcdfa64c5b0c311ffe4c2bd991e43991e82e5367792ed78bc2dca27354"
		;;
	linux/arm64)
		archive="sqlc_${version}_linux_arm64.tar.gz"
		expected="b7cae247740d0c51a1e657479e5b2d21e6fef428f596682a01bc55bf4ab8a23d"
		;;
	darwin/arm64)
		archive="sqlc_${version}_darwin_arm64.tar.gz"
		expected="21602158c99eb1f2bae197a66abfb1941d1e9e50b23125bb193349c6b1acc71e"
		;;
	*)
		echo "unsupported platform: $platform" >&2
		exit 2
		;;
esac

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
download="$work/$archive"

curl -fsSL --retry 3 --output "$download" "https://downloads.sqlc.dev/$archive"
if command -v sha256sum >/dev/null 2>&1; then
	actual="$(sha256sum "$download" | awk '{print $1}')"
else
	actual="$(shasum -a 256 "$download" | awk '{print $1}')"
fi
if [[ "$actual" != "$expected" ]]; then
	echo "sqlc checksum mismatch: got $actual" >&2
	exit 1
fi

tar -xzf "$download" -C "$work"
install -d "$output"
install -m 0755 "$work/sqlc" "$output/sqlc"
