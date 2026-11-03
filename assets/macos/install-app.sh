#!/bin/sh
# Installs AutoDoc.app from this template, so Spotlight (Cmd+Space) and the
# Dock open `autodoc --gui`. Run it again to repair or update the app.
#
#   assets/macos/install-app.sh [destination folder] (default ~/Applications)
#
# AUTODOC_BIN names the autodoc to run (default: the one on PATH).
set -eu

here=$(cd "$(dirname "$0")" && pwd)
dest=${1:-"$HOME/Applications"}
app="$dest/AutoDoc.app"
lsregister=/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister

[ "$(uname -s)" = Darwin ] || { echo "install-app.sh: macOS only" >&2; exit 1; }

# Keep the path as found: Homebrew's /opt/homebrew/bin/autodoc outlives an
# upgrade, where the Cellar path it links to does not.
bin=${AUTODOC_BIN:-$(command -v autodoc || true)}
[ -n "$bin" ] && [ -x "$bin" ] || {
	echo "install-app.sh: no autodoc on PATH; install it, or set AUTODOC_BIN" >&2
	exit 1
}
case $bin in /*) ;; *) bin="$(pwd)/$bin" ;; esac

# --diagram-reference exits 3 in a build without the window.
status=0
"$bin" --diagram-reference >/dev/null 2>&1 || status=$?
if [ "$status" -eq 3 ]; then
	echo "install-app.sh: $bin is a terminal-only build; the app needs the GUI build (README: Quick start)" >&2
	exit 1
fi

mkdir -p "$dest"
rm -rf "$app"
cp -R "$here/AutoDoc.app" "$app"
mkdir -p "$app/Contents/Resources"
cp "$here/../icon/AppIcon.icns" "$app/Contents/Resources/AppIcon.icns"
ln -s "$bin" "$app/Contents/MacOS/autodoc-bin"

# Tell Launch Services and Spotlight now, rather than at their next scan.
touch "$app"
"$lsregister" -f "$app"
mdimport "$app" 2>/dev/null || true

echo "installed $app (runs $bin --gui)"
