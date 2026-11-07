#!/bin/sh
# scripts/install/test/run.sh: the installer's cells (docs/decisions/install.md#nothing-moves-until-everything-is-checked).
#
# Each cell runs install-autodoc.sh or update.sh against a fake release made here (a file:// base URL:
# the scripts' --base-url), in a fresh HOME, and checks what they did on disk. The fake autodoc is a
# shell script that answers --version, and with --serve keeps running (the running-binary cell).
#
#   sh scripts/install/test/run.sh        (from the repo's root; Linux or macOS; needs curl, tar)
#
# It prints a PASS or FAIL line per cell and "installer cells: PASS=n FAIL=m"; its exit status is
# the number of failures (0 when all pass).
set -u

here=$(cd "$(dirname "$0")" && pwd)
scripts=$(cd "$here/.." && pwd)
repo=$(cd "$scripts/../.." && pwd)
work=$(mktemp -d 2>/dev/null || mktemp -d -t autodoc-installer-test)
trap 'rm -rf "$work"' EXIT
pass=0
fail=0

case $(uname -s) in Linux) os=linux ;; Darwin) os=darwin ;; *) echo "linux or macOS only" >&2; exit 1 ;; esac
case $(uname -m) in x86_64 | amd64) arch=amd64 ;; aarch64 | arm64) arch=arm64 ;; *) arch=$(uname -m) ;; esac
if command -v sha256sum >/dev/null 2>&1; then sum() { sha256sum "$1" | cut -d' ' -f1; }; else sum() { shasum -a 256 "$1" | cut -d' ' -f1; }; fi

ok() { pass=$((pass + 1)); echo "PASS  $1"; }
bad() { fail=$((fail + 1)); echo "FAIL  $1: $2"; }

# ── the fake release ──────────────────────────────────────────────────────────────────────────
R="$work/release"

# publish TAG adds release TAG: the fake binary for this os/arch, update.sh, install-autodoc.sh,
# the launcher assets, a source tarball, each with its .sha256.
publish() {
	tag=$1
	d="$R/download/$tag"
	mkdir -p "$d" "$work/build/$tag"
	b="$work/build/$tag/autodoc-$tag-$os-$arch"
	cat >"$b" <<EOF
#!/bin/sh
case "\${1:-}" in
--version) echo "autodoc $tag" ;;
--diagram-reference) exit 0 ;;
--serve) while :; do sleep 1; done ;;
esac
EOF
	chmod 0755 "$b"
	tar -czf "$d/autodoc-$tag-$os-$arch.tar.gz" -C "$work/build/$tag" "autodoc-$tag-$os-$arch"
	cp "$scripts/update.sh" "$d/update.sh"
	cp "$scripts/install-autodoc.sh" "$d/install-autodoc.sh"
	mkdir -p "$work/build/$tag/assets"
	cp -R "$repo/assets/icon" "$repo/assets/macos" "$work/build/$tag/assets/"
	tar -czf "$d/autodoc-$tag-assets.tar.gz" -C "$work/build/$tag" assets
	mkdir -p "$work/build/$tag/src/autodoc-$tag"
	printf 'module example\n\ngo 1.26\n' >"$work/build/$tag/src/autodoc-$tag/go.mod"
	tar -czf "$d/autodoc-$tag-src.tar.gz" -C "$work/build/$tag/src" "autodoc-$tag"
	for f in "$d"/*; do
		case $f in *.sha256) continue ;; esac
		printf '%s  %s\n' "$(sum "$f")" "$(basename "$f")" >"$f.sha256"
	done
}
latest() { printf '%s\n' "$1" >"$R/latest"; }

publish v1.0.0
publish v1.1.0
latest v1.0.0

# fresh starts a cell: a new HOME, no XDG overrides, ~/.local/bin off PATH, a dotfile per shell.
fresh() {
	H="$work/home-$1"
	rm -rf "$H"
	mkdir -p "$H"
	for rc in .profile .bashrc .zshrc; do printf '# %s\n' "$rc" >"$H/$rc"; done
	BIN="$H/.local/bin/autodoc"
	STATE="$H/.local/state/autodoc"
	ENTRY="$H/.local/share/applications/autodoc.desktop"
	APP="$H/Applications/AutoDoc.app"
}
inst() { env -u XDG_STATE_HOME -u XDG_DATA_HOME HOME="$H" PATH="/usr/bin:/bin:/usr/sbin:/sbin" sh "$scripts/install-autodoc.sh" --base-url "file://$R" "$@"; }
upd() { env -u XDG_STATE_HOME -u XDG_DATA_HOME HOME="$H" PATH="/usr/bin:/bin:/usr/sbin:/sbin" sh "$STATE/update.sh" --base-url "file://$R" "$@"; }
rec() { sed -n "s/^$1[[:space:]]*=[[:space:]]*\"\(.*\)\"/\1/p" "$STATE/install.toml" 2>/dev/null; }
launcher() { if [ "$os" = darwin ]; then echo "$APP"; else echo "$ENTRY"; fi; }

# ── the cells ─────────────────────────────────────────────────────────────────────────────────

# A fresh install: the binary, the record, update.sh, the launcher; the PATH line printed, and no rc
# file touched.
fresh install
rcs=$(cat "$H/.profile" "$H/.bashrc" "$H/.zshrc" | cksum)
out=$(inst --yes 2>&1); st=$?
if [ $st -eq 0 ] && [ "$("$BIN" --version)" = "autodoc v1.0.0" ] && [ "$(rec method)" = script ] &&
	[ "$(rec version)" = v1.0.0 ] && [ -x "$STATE/update.sh" ] && [ -e "$(launcher)" ]; then
	ok "a fresh install"
else bad "a fresh install" "status $st; $out"; fi
case $out in *'export PATH="'"$H"'/.local/bin:$PATH"'*) ok "the PATH line printed" ;; *) bad "the PATH line printed" "$out" ;; esac
[ "$(cat "$H/.profile" "$H/.bashrc" "$H/.zshrc" | cksum)" = "$rcs" ] && ok "no shell rc file touched" || bad "no shell rc file touched" "an rc file changed"
if [ "$os" = linux ]; then
	grep -q "^Exec=$BIN --gui\$" "$ENTRY" && ok "the desktop entry runs the binary --gui" || bad "the desktop entry runs the binary --gui" "$(cat "$ENTRY")"
fi

# A rerun changes nothing: the binary and the launcher byte-identical.
b1=$(sum "$BIN"); l1=$( [ -f "$(launcher)" ] && sum "$(launcher)" || ls -la "$(launcher)" | cksum )
out=$(inst --yes 2>&1); st=$?
l2=$( [ -f "$(launcher)" ] && sum "$(launcher)" || ls -la "$(launcher)" | cksum )
[ $st -eq 0 ] && [ "$(sum "$BIN")" = "$b1" ] && [ "$l2" = "$l1" ] && ok "a rerun changes nothing" || bad "a rerun changes nothing" "status $st; $out"

# An upgrade: the newest release installed, the record following.
latest v1.1.0
out=$(inst --yes 2>&1); st=$?
[ $st -eq 0 ] && [ "$("$BIN" --version)" = "autodoc v1.1.0" ] && [ "$(rec version)" = v1.1.0 ] && ok "an upgrade" || bad "an upgrade" "status $st; $out"
latest v1.0.0

# A checksum that doesn't match: refused, nothing installed.
fresh mismatch
cp "$R/download/v1.0.0/autodoc-v1.0.0-$os-$arch.tar.gz.sha256" "$work/sha.keep"
printf '%064d  x\n' 0 >"$R/download/v1.0.0/autodoc-v1.0.0-$os-$arch.tar.gz.sha256"
out=$(inst --yes 2>&1); st=$?
cp "$work/sha.keep" "$R/download/v1.0.0/autodoc-v1.0.0-$os-$arch.tar.gz.sha256"
[ $st -eq 4 ] && [ ! -e "$BIN" ] && [ ! -e "$STATE/install.toml" ] && ok "a checksum mismatch is refused, nothing installed" || bad "a checksum mismatch is refused, nothing installed" "status $st; $out"

# A launcher that runs another binary: the user declining (--yes answers no), it stays byte-identical.
fresh other-launcher
if [ "$os" = linux ]; then
	mkdir -p "$(dirname "$ENTRY")"
	printf '[Desktop Entry]\nType=Application\nName=AutoDoc\nExec=/opt/other/autodoc --gui\n' >"$ENTRY"
	before=$(sum "$ENTRY")
else
	mkdir -p "$APP/Contents/MacOS"
	ln -s /opt/other/autodoc "$APP/Contents/MacOS/autodoc-bin"
	before=$(ls -laR "$APP" | cksum)
fi
out=$(inst --yes 2>&1); st=$?
if [ "$os" = linux ]; then after=$(sum "$ENTRY"); else after=$(ls -laR "$APP" | cksum); fi
[ $st -eq 0 ] && [ "$after" = "$before" ] && ok "a launcher running another binary, declined, is kept byte-identical" || bad "a launcher running another binary, declined, is kept byte-identical" "status $st; $out"

# --no-launcher: no launcher made.
fresh no-launcher
out=$(inst --yes --no-launcher 2>&1); st=$?
[ $st -eq 0 ] && [ ! -e "$(launcher)" ] && [ -x "$BIN" ] && ok "--no-launcher adds none" || bad "--no-launcher adds none" "status $st; $out"

# The network: the tarball truncated mid-stream (its checksum fails), and the release unreachable.
fresh truncated
inst --yes >/dev/null 2>&1
latest v1.1.0
b1=$(sum "$BIN"); u1=$(sum "$STATE/update.sh")
cp "$R/download/v1.1.0/autodoc-v1.1.0-$os-$arch.tar.gz" "$work/tgz.keep"
head -c 100 "$work/tgz.keep" >"$R/download/v1.1.0/autodoc-v1.1.0-$os-$arch.tar.gz"
out=$(inst --yes 2>&1); st=$?
cp "$work/tgz.keep" "$R/download/v1.1.0/autodoc-v1.1.0-$os-$arch.tar.gz"
[ $st -eq 4 ] && [ "$(sum "$BIN")" = "$b1" ] && [ "$(sum "$STATE/update.sh")" = "$u1" ] && ok "a truncated download: exit 4, the old install untouched" || bad "a truncated download: exit 4, the old install untouched" "status $st; $out"
out=$(env -u XDG_STATE_HOME HOME="$H" PATH=/usr/bin:/bin sh "$scripts/install-autodoc.sh" --yes --version v1.1.0 --base-url "file://$work/nowhere" 2>&1); st=$?
[ $st -eq 4 ] && [ "$(sum "$BIN")" = "$b1" ] && ok "an unreachable release: exit 4, the old install untouched" || bad "an unreachable release: exit 4, the old install untouched" "status $st; $out"
latest v1.0.0

# update.sh: it runs the release's update.sh (marked here), which updates AutoDoc and installs the
# new update.sh; a dead base URL or a bad update.sh checksum exits 3 with nothing changed.
fresh update
inst --yes >/dev/null 2>&1
latest v1.1.0
cp "$R/download/v1.1.0/update.sh" "$work/upd.keep"
cp "$R/download/v1.1.0/update.sh.sha256" "$work/updsha.keep"
sed '2i\
echo "FETCHED-UPDATE-RAN"' "$work/upd.keep" >"$R/download/v1.1.0/update.sh"
printf '%s  update.sh\n' "$(sum "$R/download/v1.1.0/update.sh")" >"$R/download/v1.1.0/update.sh.sha256"
out=$(upd --yes 2>&1); st=$?
case $out in *FETCHED-UPDATE-RAN*) ran=1 ;; *) ran=0 ;; esac
[ $st -eq 0 ] && [ $ran -eq 1 ] && [ "$("$BIN" --version)" = "autodoc v1.1.0" ] && [ "$(rec version)" = v1.1.0 ] &&
	[ "$(sum "$STATE/update.sh")" = "$(sum "$R/download/v1.1.0/update.sh")" ] && ok "update.sh runs the release's update.sh and updates" || bad "update.sh runs the release's update.sh and updates" "status $st; $out"
cp "$work/upd.keep" "$R/download/v1.1.0/update.sh"
cp "$work/updsha.keep" "$R/download/v1.1.0/update.sh.sha256"

fresh update-fails
inst --yes >/dev/null 2>&1
latest v1.1.0
b1=$(sum "$BIN"); u1=$(sum "$STATE/update.sh")
out=$(env -u XDG_STATE_HOME HOME="$H" PATH=/usr/bin:/bin sh "$STATE/update.sh" --yes --base-url "file://$work/nowhere" 2>&1); st=$?
[ $st -eq 3 ] && [ "$(sum "$BIN")" = "$b1" ] && [ "$(sum "$STATE/update.sh")" = "$u1" ] && ok "update.sh with the release unreachable: exit 3, nothing changed" || bad "update.sh with the release unreachable: exit 3, nothing changed" "status $st; $out"
cp "$R/download/v1.1.0/update.sh.sha256" "$work/updsha.keep"
printf '%064d  update.sh\n' 0 >"$R/download/v1.1.0/update.sh.sha256"
out=$(upd --yes 2>&1); st=$?
cp "$work/updsha.keep" "$R/download/v1.1.0/update.sh.sha256"
[ $st -eq 3 ] && [ "$(sum "$BIN")" = "$b1" ] && [ "$(sum "$STATE/update.sh")" = "$u1" ] && ok "update.sh with a bad checksum: exit 3, nothing changed" || bad "update.sh with a bad checksum: exit 3, nothing changed" "status $st; $out"
latest v1.0.0

# update.sh refuses an AutoDoc another tool installed.
fresh refuse
inst --yes >/dev/null 2>&1
sed 's/^method .*/method       = "brew"/' "$STATE/install.toml" >"$work/rec" && cp "$work/rec" "$STATE/install.toml"
out=$(upd --yes 2>&1); st=$?
[ $st -eq 2 ] && ok "update.sh refuses an install another tool made" || bad "update.sh refuses an install another tool made" "status $st; $out"

# A running AutoDoc keeps running while its binary is swapped; the path then serves the new version.
fresh running
inst --yes >/dev/null 2>&1
"$BIN" --serve &
pid=$!
sleep 1
latest v1.1.0
out=$(inst --yes 2>&1); st=$?
if [ $st -eq 0 ] && kill -0 "$pid" 2>/dev/null && [ "$("$BIN" --version)" = "autodoc v1.1.0" ]; then
	ok "a running AutoDoc survives the swap; the path serves the new version"
else bad "a running AutoDoc survives the swap; the path serves the new version" "status $st; alive $(kill -0 "$pid" 2>/dev/null && echo yes || echo no); $out"; fi
kill "$pid" 2>/dev/null
wait "$pid" 2>/dev/null
latest v1.0.0

# No prebuilt build for this system: declined (--yes), exit 3 with nothing installed; with --build,
# a source tarball whose checksum doesn't match is refused (exit 4).
fresh nobuild
mkdir -p "$R/download/v2.0.0"
cp "$R/download/v1.0.0/update.sh" "$R/download/v1.0.0/update.sh.sha256" "$R/download/v2.0.0/"
printf 'not a tarball\n' >"$R/download/v2.0.0/autodoc-v2.0.0-src.tar.gz"
printf '%064d  autodoc-v2.0.0-src.tar.gz\n' 0 >"$R/download/v2.0.0/autodoc-v2.0.0-src.tar.gz.sha256"
out=$(inst --yes --version v2.0.0 2>&1); st=$?
[ $st -eq 3 ] && [ ! -e "$BIN" ] && ok "no prebuilt build, the build declined: exit 3, nothing installed" || bad "no prebuilt build, the build declined: exit 3, nothing installed" "status $st; $out"
out=$(inst --build --version v2.0.0 2>&1); st=$?
[ $st -eq 4 ] && [ ! -e "$BIN" ] && ok "--build with a source tarball that doesn't match its checksum: exit 4" || bad "--build with a source tarball that doesn't match its checksum: exit 4" "status $st; $out"

# Windows says so, and installs nothing.
fresh windows
mkdir -p "$work/fakeuname"
printf '#!/bin/sh\ncase "$1" in -s) echo MINGW64_NT-10.0 ;; -m) echo x86_64 ;; esac\n' >"$work/fakeuname/uname"
chmod 0755 "$work/fakeuname/uname"
out=$(env HOME="$H" PATH="$work/fakeuname:/usr/bin:/bin" sh "$scripts/install-autodoc.sh" --yes --base-url "file://$R" 2>&1); st=$?
case $out in *"doesn't run on Windows yet"*) said=1 ;; *) said=0 ;; esac
[ $st -eq 2 ] && [ $said -eq 1 ] && [ ! -e "$BIN" ] && ok "Windows: says the port is pending, installs nothing" || bad "Windows: says the port is pending, installs nothing" "status $st; $out"

echo "installer cells: PASS=$pass FAIL=$fail"
exit "$fail"
