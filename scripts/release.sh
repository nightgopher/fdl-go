#!/bin/bash
# 构建 fdl-go 全部二进制的发布包。用法：scripts/release.sh <版本号> [输出目录]
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${1:?用法：release.sh <版本号如 v0.1.0>}"
OUT="${2:-dist}"
CMDS=(fdl fdl-mcp fdl-web fdl-sync fdl-app)
TARGETS=(darwin-arm64 darwin-amd64 linux-amd64 linux-arm64)
LDFLAGS="-s -w -X github.com/nightgopher/fdl-go.Version=${VERSION}"

rm -rf "$OUT"; mkdir -p "$OUT"
for target in "${TARGETS[@]}"; do
    os="${target%-*}"; arch="${target#*-}"
    dir="$OUT/fdl-go-${VERSION}-${target}"
    mkdir -p "$dir"
    for cmd in "${CMDS[@]}"; do
        CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "$LDFLAGS" -o "$dir/$cmd" "./cmd/$cmd"
    done
    cp README.md LICENSE "$dir/"
    tar -czf "$dir.tar.gz" -C "$OUT" "fdl-go-${VERSION}-${target}"
    rm -rf "$dir"
    echo "✓ $dir.tar.gz"
done
(cd "$OUT" && shasum -a 256 *.tar.gz > checksums.txt)
echo "✓ checksums.txt"
