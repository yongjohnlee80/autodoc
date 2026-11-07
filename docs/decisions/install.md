# Install decisions

How AutoDoc is installed and updated without a package manager: two POSIX `sh` scripts attached to
every release, `install-autodoc.sh` and `update.sh`. The developer routes (`go install`, mise, a
release tarball by hand) stay as they are; these scripts are one more route, for a user who wants
AutoDoc's window and an Update button.

## Scripts over the release's tarballs

The release already builds and checksums the GUI binaries for Linux and macOS, amd64 and arm64
(`autodoc-<v>-<os>-<arch>.tar.gz` and its `.sha256`). The installer downloads the one for this
system into a temp dir, checks its checksum and its `--version`, and installs it; CI builds nothing
new for it. It works as `curl -fsSL <url> | sh` and as a downloaded file. Native packages (deb, rpm,
a dmg, a Homebrew cask) each need their own build, signing and repository, and can come later
beside this route.

Where no prebuilt binary fits the system, the installer asks before building from the release's
source tarball (`autodoc-<v>-src.tar.gz`, checksummed like the binaries; GitHub's own source
archives aren't, as their bytes can change). It needs Go of the version `go.mod` names (a system
`go`, or mise's) and, on Linux, the window system's libraries, which it checks with `pkg-config`
and lists with the distro's install line, never installing them itself. `--build` answers yes, and
`--yes` answers every question no.

On Windows it says AutoDoc doesn't run there yet and installs nothing (exit 2): AutoDoc doesn't build
for Windows today.

In the code: `scripts/install/install-autodoc.sh`, `.github/workflows/release.yml`

## A user-level install

The binary goes in `~/.local/bin/autodoc` (`--prefix` moves it), the record and update.sh in
`${XDG_STATE_HOME:-~/.local/state}/autodoc/`. Nothing needs sudo, so an update can replace the
binary from inside AutoDoc without asking for a password, and removing AutoDoc is deleting files the
user owns.

When `~/.local/bin` isn't on `PATH`, the installer prints the line to add and edits no shell rc
file: an rc edit made without asking surprises a user and breaks managed dotfiles.

In the code: `scripts/install/install-autodoc.sh`

## The install record

`install.toml` says the script installed AutoDoc, which version, and where the binary, the launcher
and update.sh are. Only the scripts write it; AutoDoc reads it and never writes it. An AutoDoc
installed another way has no record, so update.sh refuses it (exit 2) and AutoDoc offers no Update
for it: a binary another tool manages is never overwritten.

In the code: `scripts/install/install-autodoc.sh`, `scripts/install/update.sh`

## Launchers

The installer adds a launcher that runs `autodoc --gui`: on Linux a freedesktop entry in
`~/.local/share/applications/autodoc.desktop` with AutoDoc's icon, which application launchers read;
on macOS `~/Applications/AutoDoc.app`, built by the release's `install-app.sh`. A launcher that
already runs the same binary is kept as it is; one that runs another binary is replaced only when
the user agrees. `--no-launcher` adds none.

In the code: `scripts/install/install-autodoc.sh`, `assets/macos/install-app.sh`

## Nothing moves until everything is checked

Every download and its checksum land in a temp dir first. The binary is copied beside the old one
and renamed over it as the install's last step: a dropped network or a failed check leaves the old
install whole, and a running AutoDoc keeps the file it started from. Rerunning the installer
repairs or upgrades, and never duplicates a launcher.

Exit codes: 0 installed or current; 2 not supported here; 3 declined, or a build's dependency
missing; 4 a download, a checksum or the install failed, with nothing changed.

In the code: `scripts/install/install-autodoc.sh`

## update.sh runs the newest installer

update.sh doesn't carry its own copy of the install steps. When a newer release exists, it fetches
that release's update.sh (checksum-checked, into a temp dir) and runs it, so an update always runs
the newest update logic, and that one runs the release's `install-autodoc.sh --yes`: one install
path to fix and to test. update.sh never replaces itself in place; the installer installs the new
update.sh as the last step of a successful update.

Exit codes: 0 updated or current; 2 not a script install; 3 a download or a checksum failed, with
nothing changed; 4 the install failed, with the old AutoDoc untouched.

In the code: `scripts/install/update.sh`, `scripts/install/test/run.sh`
