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
DMG_ARCHS="darwin-arm64 darwin-amd64"
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
# macOS DMG：fdl.app（拖入 Applications 的卷）。
for target in $DMG_ARCHS; do
    arch="${target#*-}"
    app="$OUT/fdl.app"
    rm -rf "$app"
    mkdir -p "$app/Contents/MacOS"
    cp "$OUT/.tmp-$target/fdl-app" "$app/Contents/MacOS/fdl-app" 2>/dev/null || \
        CGO_ENABLED=0 GOOS=darwin GOARCH="$arch" go build -trimpath -ldflags "$LDFLAGS" -o "$app/Contents/MacOS/fdl-app" ./cmd/fdl-app
    cat > "$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleDisplayName</key><string>fdl</string>
	<key>CFBundleExecutable</key><string>fdl-app</string>
	<key>CFBundleIdentifier</key><string>io.github.nightgopher.fdl</string>
	<key>CFBundleInfoDictionaryVersion</key><string>6.0</string>
	<key>CFBundleName</key><string>fdl</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleShortVersionString</key><string>${VERSION#v}</string>
	<key>CFBundleVersion</key><string>${VERSION#v}</string>
	<key>LSMinimumSystemVersion</key><string>12.0</string>
	<key>NSHighResolutionCapable</key><true/>
	<key>NSPrincipalClass</key><string>NSApplication</string>
</dict>
</plist>
PLIST
    printf 'APPL????' > "$app/Contents/PkgInfo"
    vol="fdl-${VERSION}"
    hdiutil create -quiet -volname "$vol" -srcfolder "$app" -ov -format UDZO \
        "$OUT/fdl-${VERSION}-${arch}.dmg"
    rm -rf "$app"
    echo "✓ fdl-${VERSION}-${arch}.dmg"
done
cp scripts/install.sh "$OUT/"
echo "✓ checksums.txt + install.sh"
