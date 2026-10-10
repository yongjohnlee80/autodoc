#!/bin/sh
set -eu

if [ "$#" -ne 5 ]; then
  echo "usage: package-editor.sh <darwin|linux> <amd64|arm64> <version> <gui-binary> <dist-dir>" >&2
  exit 2
fi

platform=$1
arch=$2
version=$3
binary=$4
dist=$5
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
archive="autodoc-editor-$version-$platform-$arch.tar.gz"
stage="$dist/autodoc-editor-$platform-$arch"

mkdir -p "$stage"
case "$platform" in
  darwin)
    app="$stage/AutoDoc Editor.app"
    mkdir -p "$app/Contents/MacOS"
    cp "$binary" "$app/Contents/MacOS/autodoc-editor"
    chmod 755 "$app/Contents/MacOS/autodoc-editor"
    cat > "$app/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleDevelopmentRegion</key><string>en</string>
  <key>CFBundleExecutable</key><string>autodoc-editor</string>
  <key>CFBundleIdentifier</key><string>com.yongjohnlee80.autodoc-editor</string>
  <key>CFBundleName</key><string>AutoDoc Editor</string>
  <key>CFBundleDisplayName</key><string>AutoDoc Editor</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>${version#v}</string>
  <key>CFBundleVersion</key><string>${version#v}</string>
  <key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
EOF
    tar -C "$stage" -czf "$dist/$archive" "AutoDoc Editor.app"
    ;;
  linux)
    cp "$binary" "$stage/autodoc-editor"
    chmod 755 "$stage/autodoc-editor"
    cp "$root/packaging/linux/autodoc-editor.desktop" "$stage/autodoc-editor.desktop"
    tar -C "$stage" -czf "$dist/$archive" autodoc-editor autodoc-editor.desktop
    ;;
  *)
    echo "unsupported platform: $platform" >&2
    exit 2
    ;;
esac
shasum -a 256 "$dist/$archive" > "$dist/$archive.sha256"
