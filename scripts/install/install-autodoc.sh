#!/bin/sh
# install-autodoc.sh: installs AutoDoc for this user, with no sudo (docs/decisions/install.md#scripts-over-the-releases-tarballs).
#
#   curl -fsSL https://github.com/yongjohnlee80/autodoc/releases/latest/download/install-autodoc.sh | sh
#   sh install-autodoc.sh [--version vX.Y.Z] [--yes] [--no-launcher] [--prefix DIR] [--base-url URL]
#
# It finds the newest release (or the one --version names), downloads its GUI build for this system,
# checks it against the release's sha256, and installs it as PREFIX/bin/autodoc (PREFIX defaults to
# ~/.local). It adds a launcher (AutoDoc.app on macOS, a desktop entry on Linux), writes the install
# record AutoDoc reads (STATE/install.toml, STATE = ${XDG_STATE_HOME:-~/.local/state}/autodoc) and
# installs update.sh beside it. Run again, it repairs or upgrades: nothing is duplicated. Where no
# prebuilt build fits, it offers to build one from the release's source.
#
# Exit codes: 0 installed or current; 1 usage; 2 not available on this system; 3 declined, or no way
# to build; 4 a download, a checksum or the install failed (what was installed before is untouched).
#
# It never uses sudo and never edits a shell's rc file: what to add to PATH is printed instead.
set -eu

REPO_RELEASES=https://github.com/yongjohnlee80/autodoc/releases
base=${AUTODOC_RELEASES:-$REPO_RELEASES}
want_version=""
assume_yes=0
build=0
launcher=1
prefix="$HOME/.local"

say() { printf '%s\n' "$*"; }
warn() { printf 'install-autodoc: %s\n' "$*" >&2; }
die() {
	code=$1
	shift
	warn "$*"
	exit "$code"
}

usage() {
	cat <<'EOF'
install-autodoc.sh: install or update AutoDoc for this user (no sudo)

  --version vX.Y.Z   install that release (default: the newest)
  --yes              ask nothing: no build from source, an existing launcher kept
  --build            where no prebuilt build fits, build from the release's source without asking
  --no-launcher      don't add AutoDoc.app or a desktop entry
  --prefix DIR       install under DIR (default ~/.local): DIR/bin/autodoc
  --base-url URL     where the releases are (default the GitHub releases)
EOF
}

# ask prints a question and answers yes only for y or yes. With --yes, or no terminal to ask on, the
# answer is no: every question this script asks is one whose no is the safe answer.
ask() {
	[ "$assume_yes" -eq 0 ] || return 1
	{ : </dev/tty; } 2>/dev/null || return 1
	printf '%s [y/N] ' "$1" >/dev/tty
	read -r answer </dev/tty || return 1
	case $answer in y | Y | yes | YES | Yes) return 0 ;; esac
	return 1
}

have() { command -v "$1" >/dev/null 2>&1; }

# fetch URL FILE downloads URL into FILE (a file:// URL works too, as the tests use).
fetch() { curl -fsSL --retry 2 -o "$2" "$1"; }

# latest prints the newest release's tag: GitHub's releases/latest redirect, or a "latest" file at a
# base URL that isn't GitHub's (a mirror, or the tests' fake release).
latest() {
	case $base in
	https://github.com/*)
		url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$base/latest") || return 1
		printf '%s\n' "${url##*/}"
		;;
	*)
		fetch "$base/latest" "$tmp/latest" || return 1
		tr -d ' \r\n' <"$tmp/latest"
		;;
	esac
}

# verified ASSET downloads the release's ASSET and ASSET.sha256 into $tmp and checks one against the
# other. A download that fails returns 1; a checksum that doesn't match returns 2. Nothing outside $tmp
# is touched either way.
verified() {
	fetch "$base/download/$version/$1" "$tmp/$1" 2>/dev/null || return 1
	fetch "$base/download/$version/$1.sha256" "$tmp/$1.sha256" 2>/dev/null || return 1
	want=$(cut -d' ' -f1 <"$tmp/$1.sha256")
	got=$(sha256 "$tmp/$1")
	if [ -z "$want" ] || [ "$want" != "$got" ]; then
		warn "$1: its checksum doesn't match the release's ($got, want ${want:-none})"
		return 2
	fi
}

# exists ASSET reports whether the release has ASSET, without downloading it.
exists() {
	case $base in
	file://*) [ -f "${base#file://}/download/$version/$1" ] ;;
	*) curl -fsSLI -o /dev/null "$base/download/$version/$1" 2>/dev/null ;;
	esac
}

# version_of BIN prints the vX.Y.Z BIN's --version names, "" when it names none.
version_of() {
	"$1" --version 2>/dev/null | sed -n 's/.*\(v[0-9][0-9]*\.[0-9][0-9.]*[^[:space:]]*\).*/\1/p' | head -n 1 || true
}

# ── building from source (no prebuilt build for this system) ─────────────────────────────────
# Linux GUI's libraries, as pkg-config names them (release.yml installs their -dev packages).
LINUX_GUI_PKGS="wayland-client wayland-egl wayland-cursor x11 x11-xcb xkbcommon xkbcommon-x11 egl glesv2 xcursor xfixes vulkan"

# distro_line prints the command that installs the Linux GUI's libraries on this distro.
distro_line() {
	if have apt-get; then
		say "sudo apt-get install -y golang libwayland-dev libx11-dev libx11-xcb-dev libxkbcommon-x11-dev libgles2-mesa-dev libegl1-mesa-dev libxcursor-dev libxfixes-dev libvulkan-dev pkg-config"
	elif have dnf; then
		say "sudo dnf install -y golang wayland-devel libX11-devel libxkbcommon-x11-devel mesa-libGLES-devel mesa-libEGL-devel libXcursor-devel libXfixes-devel vulkan-loader-devel pkgconf-pkg-config"
	elif have pacman; then
		say "sudo pacman -S --needed go wayland libx11 libxkbcommon-x11 mesa libxcursor libxfixes vulkan-icd-loader vulkan-headers pkgconf"
	elif have zypper; then
		say "sudo zypper install go wayland-devel libX11-devel libxkbcommon-x11-devel Mesa-libGLESv2-devel Mesa-libEGL-devel libXcursor-devel libXfixes-devel vulkan-devel pkg-config"
	else
		say "(your distro's packages for: $LINUX_GUI_PKGS, and Go)"
	fi
}

# build_from_source builds the release's GUI build from its source tarball (checksum-verified) and
# prints the built binary's path. Anything missing stops it with what to install; nothing half-
# installs, since the build happens in $tmp.
build_from_source() {
	src="autodoc-$version-src.tar.gz"
	verified "$src" || {
		warn "the release's source ($src) didn't download or didn't match its checksum"
		return 4
	}
	mkdir -p "$tmp/src"
	tar -xzf "$tmp/$src" -C "$tmp/src" || return 4
	dir=$(find "$tmp/src" -maxdepth 2 -name go.mod -exec dirname {} \; | head -n 1)
	[ -n "$dir" ] || {
		warn "$src holds no go.mod"
		return 4
	}
	gover=$(sed -n 's/^go \([0-9.]*\).*/\1/p' "$dir/go.mod" | head -n 1)
	gobin=""
	if have mise; then
		gobin="mise exec go@$gover -- go"
	elif have go; then
		gobin=go
	else
		warn "building AutoDoc needs Go $gover: install it (or mise, which installs it for the build)"
		[ "$os" = linux ] && warn "on this system: $(distro_line)"
		return 3
	fi
	if [ "$os" = linux ]; then
		have pkg-config || {
			warn "building AutoDoc's window needs pkg-config and these libraries: $(distro_line)"
			return 3
		}
		missing=""
		for p in $LINUX_GUI_PKGS; do pkg-config --exists "$p" 2>/dev/null || missing="$missing $p"; done
		[ -z "$missing" ] || {
			warn "missing for AutoDoc's window:$missing"
			warn "install them with: $(distro_line)"
			return 3
		}
	fi
	say "Building AutoDoc $version (a few minutes)..." >&2
	out="$tmp/autodoc-$version-$os-$arch"
	(cd "$dir" && CGO_ENABLED=1 $gobin build -tags gui -trimpath \
		-ldflags "-s -w -X main.version=$version -X main.buildDate=$(date -u +%F)" \
		-o "$out" ./cmd/autodoc) >&2 || {
		warn "the build failed: nothing was installed"
		return 4
	}
	printf '%s\n' "$out"
}

# ── the launchers ─────────────────────────────────────────────────────────────────────────────
launcher_path=""

# assets fetches and unpacks the release's launcher assets (the icon, the macOS app template).
assets() {
	[ -d "$tmp/assets/assets" ] && return 0
	verified "autodoc-$version-assets.tar.gz" || return 1
	mkdir -p "$tmp/assets"
	tar -xzf "$tmp/autodoc-$version-assets.tar.gz" -C "$tmp/assets"
}

# desktop_entry adds ~/.local/share/applications/autodoc.desktop (Linux). An entry that runs this
# binary is kept as it is; one that runs another is replaced only when the user says so.
desktop_entry() {
	apps="${XDG_DATA_HOME:-$HOME/.local/share}/applications"
	entry="$apps/autodoc.desktop"
	if [ -f "$entry" ]; then
		exec_line=$(sed -n 's/^Exec=//p' "$entry" | head -n 1)
		case $exec_line in
		"$bin"*)
			launcher_path=$entry
			say "Kept the launcher $entry (it runs $bin)."
			return 0
			;;
		esac
		say "A launcher already exists, $entry, running: $exec_line"
		if ! ask "Replace it with one that runs $bin?"; then
			say "Kept it as it is."
			launcher_path=$entry
			return 0
		fi
	fi
	icon=accessories-text-editor
	if assets 2>/dev/null; then
		icons="${XDG_DATA_HOME:-$HOME/.local/share}/icons/hicolor/512x512/apps"
		mkdir -p "$icons"
		cp "$tmp/assets/assets/icon/AppIcon.png" "$icons/autodoc.png"
		icon=autodoc
	fi
	mkdir -p "$apps"
	cat >"$apps/.autodoc.desktop.new.$$" <<EOF
[Desktop Entry]
Type=Application
Name=AutoDoc
GenericName=Document search and editor
Comment=Search and edit your knowledge bases
Exec=$bin --gui
Icon=$icon
Terminal=false
StartupWMClass=autodoc
Categories=Utility;TextEditor;
Keywords=kb;notes;markdown;search;docs;
EOF
	mv -f "$apps/.autodoc.desktop.new.$$" "$entry"
	have update-desktop-database && update-desktop-database "$apps" >/dev/null 2>&1 || true
	launcher_path=$entry
	say "Added the launcher $entry."
}

# mac_app adds ~/Applications/AutoDoc.app through the release's install-app.sh (macOS). An app
# that runs this binary is kept; one that runs another is replaced only when the user says so.
mac_app() {
	app="$HOME/Applications/AutoDoc.app"
	if [ -d "$app" ]; then
		runs=$(readlink "$app/Contents/MacOS/autodoc-bin" 2>/dev/null || true)
		if [ "$runs" = "$bin" ]; then
			launcher_path=$app
			say "Kept $app (it runs $bin)."
			return 0
		fi
		say "AutoDoc.app already exists, $app, running: ${runs:-another autodoc}"
		if ! ask "Replace it with one that runs $bin?"; then
			say "Kept it as it is."
			launcher_path=$app
			return 0
		fi
	fi
	assets || {
		warn "the launcher's files didn't download: AutoDoc is installed, without AutoDoc.app"
		return 0
	}
	AUTODOC_BIN=$bin sh "$tmp/assets/assets/macos/install-app.sh" "$HOME/Applications" || {
		warn "install-app.sh failed: AutoDoc is installed, without AutoDoc.app"
		return 0
	}
	launcher_path=$app
}

# ── the record and update.sh ──────────────────────────────────────────────────────────────────
# install_update fetches the release's update.sh (verified) into STATE; a failure keeps the one there.
install_update() {
	verified update.sh || {
		warn "update.sh didn't download: AutoDoc is installed, and update.sh is as it was"
		return 0
	}
	mkdir -p "$statedir"
	cp "$tmp/update.sh" "$statedir/.update.sh.new.$$"
	chmod 0755 "$statedir/.update.sh.new.$$"
	mv -f "$statedir/.update.sh.new.$$" "$statedir/update.sh"
}

# record_value KEY prints a key's value from the install record, "" when there's none.
record_value() {
	[ -f "$record" ] || return 0
	sed -n "s/^$1[[:space:]]*=[[:space:]]*\"\(.*\)\"[[:space:]]*\$/\1/p" "$record" | head -n 1
}

# write_record writes STATE/install.toml: the one place AutoDoc learns the script installed it.
write_record() {
	now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
	since=$(record_value installed_at)
	mkdir -p "$statedir"
	cat >"$statedir/.install.toml.new.$$" <<EOF
# Written by install-autodoc.sh and update.sh; AutoDoc reads it (its About dialog's Update).
method       = "script"
version      = "$version"
channel      = "stable"
bin          = "$bin"
launcher     = "$launcher_path"
update       = "$statedir/update.sh"
installed_at = "${since:-$now}"
updated_at   = "$now"
EOF
	mv -f "$statedir/.install.toml.new.$$" "$record"
}

# ── main ──────────────────────────────────────────────────────────────────────────────────────
while [ $# -gt 0 ]; do
	case $1 in
	--version)
		[ $# -ge 2 ] || die 1 "--version needs a value"
		want_version=$2
		shift 2
		;;
	--version=*)
		want_version=${1#*=}
		shift
		;;
	--yes | -y)
		assume_yes=1
		shift
		;;
	--no-launcher)
		launcher=0
		shift
		;;
	--build)
		build=1
		shift
		;;
	--prefix)
		[ $# -ge 2 ] || die 1 "--prefix needs a value"
		prefix=$2
		shift 2
		;;
	--prefix=*)
		prefix=${1#*=}
		shift
		;;
	--base-url)
		[ $# -ge 2 ] || die 1 "--base-url needs a value"
		base=$2
		shift 2
		;;
	--base-url=*)
		base=${1#*=}
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		usage >&2
		die 1 "unknown option: $1"
		;;
	esac
done
base=${base%/}
bindir="$prefix/bin"
bin="$bindir/autodoc"
statedir="${XDG_STATE_HOME:-$HOME/.local/state}/autodoc"
record="$statedir/install.toml"

os=""
case $(uname -s) in
Linux) os=linux ;;
Darwin) os=darwin ;;
MINGW* | MSYS* | CYGWIN* | Windows_NT)
	die 2 "AutoDoc doesn't run on Windows yet: its port is under way. Until then it runs under WSL, where this script works as on Linux."
	;;
*) die 2 "AutoDoc has no build for $(uname -s)." ;;
esac
case $(uname -m) in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) arch=$(uname -m) ;;
esac

have curl || die 4 "curl is needed to download AutoDoc"
have tar || die 4 "tar is needed to unpack AutoDoc"
if have sha256sum; then
	sha256() { sha256sum "$1" | cut -d' ' -f1; }
elif have shasum; then
	sha256() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
	die 4 "sha256sum or shasum is needed to check the download"
fi

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t autodoc-install)
trap 'rm -rf "$tmp"' EXIT
trap 'rm -rf "$tmp"; exit 4' HUP INT TERM

version=$want_version
[ -n "$version" ] || version=$(latest) || die 4 "couldn't reach $base to find the newest AutoDoc"
case $version in v*) ;; *) version="v$version" ;; esac

installed=""
[ -x "$bin" ] && installed=$(version_of "$bin")

if [ "$installed" = "$version" ]; then
	say "AutoDoc $version is installed already ($bin)."
else
	asset="autodoc-$version-$os-$arch.tar.gz"
	if exists "$asset"; then
		verified "$asset" || die 4 "$asset didn't download or didn't match its checksum: nothing was installed"
		mkdir -p "$tmp/unpack"
		tar -xzf "$tmp/$asset" -C "$tmp/unpack" || die 4 "couldn't unpack $asset: nothing was installed"
		new="$tmp/unpack/autodoc-$version-$os-$arch"
		[ -f "$new" ] || die 4 "$asset holds no autodoc-$version-$os-$arch: nothing was installed"
	else
		# Every release has update.sh: if that isn't there either, it's the network or the URL, not a
		# missing build, and nothing is offered.
		exists update.sh || die 4 "couldn't reach the $version release at $base: nothing was installed"
		say "There's no prebuilt AutoDoc $version for $os/$arch."
		if [ "$build" -eq 0 ] && ! ask "Build it from the release's source? It needs Go and, on Linux, the window system's libraries."; then
			say "Not built. Other ways to install AutoDoc: https://github.com/yongjohnlee80/autodoc#install"
			exit 3
		fi
		status=0
		new=$(build_from_source) || status=$?
		[ "$status" -eq 0 ] || exit "$status"
	fi
	chmod 0755 "$new"
	got=$(version_of "$new")
	[ "$got" = "$version" ] || die 4 "the downloaded AutoDoc says \"${got:-no version}\", not $version: nothing was installed"
	mkdir -p "$bindir"
	cp "$new" "$bindir/.autodoc.new.$$"
	chmod 0755 "$bindir/.autodoc.new.$$"
	# One rename swaps the binary: a running AutoDoc keeps the file it started from, and an
	# interrupted install leaves the old binary whole.
	if ! mv -f "$bindir/.autodoc.new.$$" "$bin"; then
		rm -f "$bindir/.autodoc.new.$$"
		die 4 "couldn't install $bin"
	fi
	say "Installed AutoDoc $version as $bin."
fi

if [ "$launcher" -eq 1 ]; then
	case $os in
	linux) desktop_entry ;;
	darwin) mac_app ;;
	esac
else
	launcher_path=$(record_value launcher)
fi

install_update
write_record

case ":$PATH:" in
*":$bindir:"*) ;;
*)
	say ""
	say "$bindir isn't on your PATH. To run autodoc from a terminal, add this line to your shell's rc file:"
	say "  export PATH=\"$bindir:\$PATH\""
	;;
esac
if [ -n "$installed" ] && [ "$installed" != "$version" ]; then
	say "AutoDoc $installed may still be running: restart it to use $version."
fi
say "Done. Update later from AutoDoc's About dialog, or run: $statedir/update.sh"
