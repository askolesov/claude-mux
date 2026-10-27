#!/usr/bin/env bash
# Build claude-mux.app, a universal (arm64 + amd64) Mac app around the hub.
#   scripts/macapp.sh <version> <out dir>
# Runs on a Mac: the window needs cgo and the macOS SDK.
set -euo pipefail

version=$1
out=$2
dir=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
app=$out/claude-mux.app

export CGO_ENABLED=1 CGO_CXXFLAGS=-Wno-deprecated-literal-operator MACOSX_DEPLOYMENT_TARGET=11.0
for arch in arm64 amd64; do
	GOARCH=$arch go -C "$dir" build -trimpath -ldflags="-s -X main.version=$version" -o "$tmp/claude-mux-$arch" ./cmd/hub
done

mkdir -p "$app/Contents/MacOS"
lipo -create -output "$app/Contents/MacOS/claude-mux" "$tmp/claude-mux-arm64" "$tmp/claude-mux-amd64"
cat >"$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key><string>claude-mux</string>
	<key>CFBundleDisplayName</key><string>claude-mux</string>
	<key>CFBundleIdentifier</key><string>io.github.askolesov.claude-mux</string>
	<key>CFBundleExecutable</key><string>claude-mux</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleShortVersionString</key><string>${version#v}</string>
	<key>CFBundleVersion</key><string>${version#v}</string>
	<key>LSMinimumSystemVersion</key><string>11.0</string>
	<key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
PLIST
# Unsigned, so at least signed ad hoc: Apple silicon runs nothing less.
codesign --force --deep --sign - "$app"
rm -r "$tmp"
