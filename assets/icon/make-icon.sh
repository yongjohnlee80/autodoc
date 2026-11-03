#!/bin/sh
# Rebuilds AppIcon.png and AppIcon.icns from make-icon.swift (macOS: swift,
# sips and iconutil ship with the Xcode command line tools).
set -eu
cd "$(dirname "$0")"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
swift make-icon.swift AppIcon.png
mkdir "$tmp/AppIcon.iconset"
for s in 16 32 128 256 512; do
	sips -z "$s" "$s" AppIcon.png --out "$tmp/AppIcon.iconset/icon_${s}x${s}.png" >/dev/null
	sips -z $((s * 2)) $((s * 2)) AppIcon.png --out "$tmp/AppIcon.iconset/icon_${s}x${s}@2x.png" >/dev/null
done
iconutil -c icns "$tmp/AppIcon.iconset" -o AppIcon.icns
