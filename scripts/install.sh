#!/bin/sh
# fdl-go 一键安装：下载 GitHub Release 最新包，装五件套到 ~/bin（或参数目录）。
# 用法：curl -fsSL https://github.com/nightgopher/fdl-go/releases/latest/download/install.sh | sh
#      install.sh [安装目录] [版本号，默认 latest]
set -eu

REPO="nightgopher/fdl-go"
DEST="${1:-$HOME/bin}"
VERSION="${2:-latest}"
OS="$(uname -s)"; ARCH="$(uname -m)"
case "$OS:$ARCH" in
    Darwin:arm64)  TARGET=darwin-arm64 ;;
    Darwin:*)      TARGET=darwin-amd64 ;;
    Linux:x86_64)  TARGET=linux-amd64 ;;
    Linux:aarch64) TARGET=linux-arm64 ;;
    *) echo "不支持的平台：$OS $ARCH" >&2; exit 1 ;;
esac

# 最新版本号
if [ "$VERSION" = "latest" ]; then
    VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | grep -o '"tag_name": *"[^"]*"' | head -1 | sed 's/.*"v/v/;s/"//')
    [ -n "$VERSION" ] || { echo "拿不到最新版本号" >&2; exit 1; }
fi

echo "→ fdl-go $VERSION ($TARGET) → $DEST"
mkdir -p "$DEST"
TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT
curl -fSL --retry 3 -o "$TMP/pkg.tar.gz" \
    "https://github.com/$REPO/releases/download/v${VERSION#v}/fdl-go-v${VERSION#v}-$TARGET.tar.gz"
tar xzf "$TMP/pkg.tar.gz" -C "$TMP"
BIN="$TMP/fdl-go-v${VERSION#v}-$TARGET"
for cmd in fdl fdl-app fdl-mcp fdl-web fdl-sync; do
    [ -f "$BIN/$cmd" ] && install -m 0755 "$BIN/$cmd" "$DEST/$cmd"
done
echo "✓ 已安装：$(ls "$BIN" | grep -v -e README -e LICENSE | tr '\n' ' ')"
case ":$PATH:" in
    *":$DEST:"*) ;;
    *) echo "⚠ $DEST 不在 PATH 里，加一行：export PATH=\"$DEST:\$PATH\"" ;;
esac
echo "试用：fdl -version && fdl-app"
