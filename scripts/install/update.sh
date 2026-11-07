#!/bin/sh
# update.sh: updates an AutoDoc that install-autodoc.sh installed (docs/decisions/install.md#updatesh-runs-the-newest-installer).
#
#   ~/.local/state/autodoc/update.sh [--yes] [--base-url URL]
#
# AutoDoc's About dialog runs it in the terminal panel. It reads the install record beside it, and
# refuses an AutoDoc another tool installed (update that one its own way). When a newer release
# exists, it first runs that release's update.sh (fetched and checksum-verified into a temp dir,
# never replacing this file in place), which runs the release's install-autodoc.sh: one install path,
# which also installs the new update.sh here as its last step.
#
# A dropped network never leaves a broken install: every download and its checksum land in a temp
# dir before anything moves, and the binary's swap is the install's last step.
#
# Exit codes: 0 updated or current; 2 not an AutoDoc install-autodoc.sh made; 3 a download or a
# checksum failed (nothing changed); 4 the install failed (the old AutoDoc is untouched).
set -eu

REPO_RELEASES=https://github.com/yongjohnlee80/autodoc/releases
base=${AUTODOC_RELEASES:-$REPO_RELEASES}
pass_yes=""

say() { printf '%s\n' "$*"; }
warn() { printf 'update: %s\n' "$*" >&2; }
die() {
	code=$1
	shift
	warn "$*"
	exit "$code"
}
have() { command -v "$1" >/dev/null 2>&1; }

while [ $# -gt 0 ]; do
	case $1 in
	--yes | -y)
		pass_yes=--yes
		shift
		;;
	--base-url)
		[ $# -ge 2 ] || die 2 "--base-url needs a value"
		base=$2
		shift 2
		;;
	--base-url=*)
		base=${1#*=}
		shift
		;;
	-h | --help)
		say "update.sh: update AutoDoc to the newest release ([--yes] [--base-url URL])"
		exit 0
		;;
	*) die 2 "unknown option: $1" ;;
	esac
done
base=${base%/}

statedir="${XDG_STATE_HOME:-$HOME/.local/state}/autodoc"
record="$statedir/install.toml"
record_value() {
	[ -f "$record" ] || return 0
	sed -n "s/^$1[[:space:]]*=[[:space:]]*\"\(.*\)\"[[:space:]]*\$/\1/p" "$record" | head -n 1
}

[ -f "$record" ] || die 2 "no install record at $record: this AutoDoc wasn't installed by install-autodoc.sh, so update it the way it was installed"
[ "$(record_value method)" = script ] || die 2 "$record doesn't say install-autodoc.sh installed AutoDoc: update it the way it was installed"
current=$(record_value version)
bin=$(record_value bin)
prefix=$(dirname "$(dirname "${bin:-$HOME/.local/bin/autodoc}")")

have curl || die 3 "curl is needed to update AutoDoc"
if have sha256sum; then
	sha256() { sha256sum "$1" | cut -d' ' -f1; }
elif have shasum; then
	sha256() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
	die 3 "sha256sum or shasum is needed to check the download"
fi

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t autodoc-update)
trap 'rm -rf "$tmp"' EXIT
trap 'rm -rf "$tmp"; exit 3' HUP INT TERM
fetch() { curl -fsSL --retry 2 -o "$2" "$1"; }

case $base in
https://github.com/*)
	url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$base/latest") || die 3 "couldn't reach $base to find the newest AutoDoc"
	latest=${url##*/}
	;;
*)
	fetch "$base/latest" "$tmp/latest" || die 3 "couldn't reach $base to find the newest AutoDoc"
	latest=$(tr -d ' \r\n' <"$tmp/latest")
	;;
esac
[ -n "$latest" ] || die 3 "couldn't tell the newest AutoDoc from $base"

if [ "$current" = "$latest" ]; then
	say "AutoDoc is up to date ($current)."
	exit 0
fi

# verified ASSET: the release's ASSET and its .sha256, into $tmp, matched; 3 when either fails.
verified() {
	fetch "$base/download/$latest/$1" "$tmp/$1" 2>/dev/null || die 3 "$1 didn't download: nothing changed"
	fetch "$base/download/$latest/$1.sha256" "$tmp/$1.sha256" 2>/dev/null || die 3 "$1.sha256 didn't download: nothing changed"
	want=$(cut -d' ' -f1 <"$tmp/$1.sha256")
	[ -n "$want" ] && [ "$want" = "$(sha256 "$tmp/$1")" ] || die 3 "$1 doesn't match its checksum: nothing changed"
}

# The newest update logic runs: this release's update.sh, from the temp dir, unless this is it.
if [ "${UPDATE_SH_IS_LATEST:-}" != "$latest" ]; then
	verified update.sh
	# exec replaces this shell, so its EXIT trap never runs: the fetched script goes to a dir of its
	# own, which the exec'd update.sh removes when it ends (AUTODOC_UPDATE_TMP).
	keep=$(mktemp -d 2>/dev/null || mktemp -d -t autodoc-update)
	cp "$tmp/update.sh" "$keep/update.sh"
	chmod 0755 "$keep/update.sh"
	rm -rf "$tmp"
	set -- "$keep/update.sh" --base-url "$base"
	[ -z "$pass_yes" ] || set -- "$@" "$pass_yes"
	UPDATE_SH_IS_LATEST=$latest AUTODOC_UPDATE_TMP=$keep exec "$@"
fi
[ -z "${AUTODOC_UPDATE_TMP:-}" ] || trap 'rm -rf "$tmp" "$AUTODOC_UPDATE_TMP"' EXIT

say "Updating AutoDoc $current to $latest..."
verified install-autodoc.sh
status=0
sh "$tmp/install-autodoc.sh" --version "$latest" --base-url "$base" --prefix "$prefix" $pass_yes || status=$?
case $status in
0) ;;
3 | 4) exit 4 ;;
*) exit "$status" ;;
esac
say "AutoDoc updated to $latest. Restart AutoDoc to use it."
