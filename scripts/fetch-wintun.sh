#!/usr/bin/env bash
# Download the pinned Wintun release and install wintun.dll next to the
# Windows binaries.
#
#   ./scripts/fetch-wintun.sh [arch] [dest]
#
# arch: amd64 (default), arm64, x86, arm
# dest: directory to place wintun.dll and its license (default: dist)
set -euo pipefail

version="0.14.1"
sha256="07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51"
arch="${1:-amd64}"
dest="${2:-dist}"
url="https://www.wintun.net/builds/wintun-${version}.zip"

for tool in curl unzip sha256sum; do
	command -v "$tool" >/dev/null || {
		echo "fetch-wintun: $tool is required" >&2
		exit 1
	}
done

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "fetching wintun ${version} (${arch})"
curl -fsSL -o "$tmp/wintun.zip" "$url"
echo "${sha256}  $tmp/wintun.zip" | sha256sum -c - >/dev/null
unzip -q "$tmp/wintun.zip" -d "$tmp"

src="$tmp/wintun/bin/${arch}/wintun.dll"
if [[ ! -f $src ]]; then
	echo "fetch-wintun: ${arch} is not in the archive (amd64, arm64, x86, arm)" >&2
	exit 1
fi

mkdir -p "$dest"
cp "$src" "$dest/wintun.dll"
cp "$tmp/wintun/LICENSE.txt" "$dest/wintun-LICENSE.txt"
echo "installed $dest/wintun.dll ($(wc -c <"$dest/wintun.dll") bytes) and $dest/wintun-LICENSE.txt"
