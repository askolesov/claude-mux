#!/usr/bin/env bash
# Regenerate every icon file derived from assets/icon/svg/*.svg:
#   scripts/icons.sh
# Writes:
#   assets/icon/png/icon-<size>.png   rendered sizes
#   assets/icon/AppIcon.icns          the Mac app's icon (scripts/macapp.sh)
#   cmd/hub/rsrc_windows_*.syso       the Windows hub's icon, linked by go build
# Needs rsvg-convert (brew install librsvg), iconutil (macOS) and Go, which
# runs go-winres from assets/icon/winres.json.
set -euo pipefail

dir=$(cd "$(dirname "$0")/.." && pwd)
svg=$dir/assets/icon/svg
png=$dir/assets/icon/png
tmp=$(mktemp -d)

# icon-small.svg up to 32 px, where the full one's three layers blur.
mkdir -p "$png"
for size in 16 24 32 48 64 128 256 512 1024; do
	src=$svg/icon.svg
	[ "$size" -le 32 ] && src=$svg/icon-small.svg
	rsvg-convert -w "$size" -h "$size" -o "$png/icon-$size.png" "$src"
done

# iconutil wants each size under its Apple name; NAME@2x is twice NAME.
set=$tmp/AppIcon.iconset
mkdir "$set"
for size in 16 32 128 256 512; do
	cp "$png/icon-$size.png" "$set/icon_${size}x$size.png"
	cp "$png/icon-$((size * 2)).png" "$set/icon_${size}x${size}@2x.png"
done
iconutil -c icns -o "$dir/assets/icon/AppIcon.icns" "$set"

go run github.com/tc-hib/go-winres@v0.3.3 make --in "$dir/assets/icon/winres.json" \
	--out "$dir/cmd/hub/rsrc" --arch amd64,arm64
rm -r "$tmp"
